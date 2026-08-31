// Package genericrest provides a reusable, hand-written rest.Storage
// implementation that talks directly to a shared storage.Interface (this
// repo's StorageImpl), as a parallel, per-resource-gated alternative to
// k8s.io/apiserver's etcd-shaped genericregistry.Store.
//
// It is deliberately configured the same way genericregistry.Store itself
// is -- a struct with function-value and interface-typed fields (NewFunc,
// NewListFunc, PredicateFunc, Strategy, ...), NOT Go generic type
// parameters -- so it can be shared across resource packages without
// fighting generics for interface-typed parameters. Each resource package
// (e.g. pkg/registry/softwarecomposition/knownservers,
// .../openvulnerabilityexchange) provides only its own thin, type-specific
// configuration (New/NewList, key-func choice via Strategy.NamespaceScoped,
// predicate, qualified resource names, strategy value) and gets the rest of
// this behavior for free.
//
// This is the Phase 4 deliverable described in
// docs/features/generic-rest-storage-phase4.md: most of what a
// genericregistry.Store-based NewREST provides for free is actually
// resource-agnostic once objects are manipulated via meta.Accessor
// reflection -- Delete, DeleteCollection, markAsDeleting,
// finalizeDeleteStatus, and the Update/tryUpdate closure's
// BeforeUpdate/conflict/dry-run logic all generalize directly. Only the
// handful of genuinely resource-specific pieces are left to each resource
// package: New()/NewList(), the key-function choice (cluster-scoped vs.
// namespaced, chosen the same way genericregistry.Store.CompleteWithOptions
// does, vendor store.go:1514-1586), the label/field selector predicate
// function, the qualified resource names, and the strategy value itself.
package genericrest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/validation"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apimachinery/pkg/watch"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/generic"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage"
	storeerr "k8s.io/apiserver/pkg/storage/errors"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/apiserver/pkg/util/dryrun"
	"sigs.k8s.io/structured-merge-diff/v6/fieldpath"
)

// OptimisticLockErrorMsg matches genericregistry.OptimisticLockErrorMsg
// (vendor store.go:262) so a resourceVersion conflict produces the same
// error message old and new implementations agree on.
const OptimisticLockErrorMsg = "the object has been modified; please apply your changes to the latest version and try again"

// maxNameGenerationCreateAttempts mirrors vendor store.go:440 -- the number
// of times Create retries when metadata.generateName is set and a generated
// name collides with an existing object.
const maxNameGenerationCreateAttempts = 8

// errEmptiedFinalizers and errDeleteNow are internal sentinels mirroring
// vendor store.go:873-875, used to signal from inside a tryUpdate/delete
// closure that the caller should fall through to a hard delete.
var (
	errEmptiedFinalizers = errors.New("emptied finalizers")
	errDeleteNow         = errors.New("delete now")
)

// Strategy is the minimal interface this package needs from a resource's
// strategy value. It is a superset of rest.RESTCreateStrategy and
// rest.RESTUpdateStrategy (which have overlapping but not identical method
// sets), so any concrete strategy that already satisfies both -- as every
// resource strategy in this repo does -- also satisfies this interface, and
// a Strategy value passed as either of those narrower interfaces to
// rest.BeforeCreate/rest.BeforeUpdate/rest.BeforeDelete works exactly as it
// would against genericregistry.Store.
type Strategy interface {
	runtime.ObjectTyper
	names.NameGenerator

	NamespaceScoped() bool
	PrepareForCreate(ctx context.Context, obj runtime.Object)
	Validate(ctx context.Context, obj runtime.Object) field.ErrorList
	WarningsOnCreate(ctx context.Context, obj runtime.Object) []string
	Canonicalize(obj runtime.Object)
	AllowCreateOnUpdate() bool
	PrepareForUpdate(ctx context.Context, obj, old runtime.Object)
	ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList
	WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string
	AllowUnconditionalUpdate() bool
}

// Store is a hand-written rest.Storage implementation, generic over any
// resource type via meta.Accessor reflection and the function/interface
// values below -- mirroring exactly how genericregistry.Store itself is
// configured (NewFunc, NewListFunc, PredicateFunc, Strategy fields, etc.),
// not Go generic type parameters.
//
// A resource package constructs one of these via NewStore with its own
// type-specific configuration; everything else (Create/Update/Delete/List/
// Watch/DeleteCollection and all their generic.Store-equivalent behaviors)
// is provided here, once, for every resource that uses it.
type Store struct {
	// NewFunc returns a new, empty instance of the resource's concrete type.
	NewFunc func() runtime.Object
	// NewListFunc returns a new, empty instance of the resource's concrete
	// list type.
	NewListFunc func() runtime.Object
	// PredicateFunc builds the label/field selector predicate used by
	// List/Watch/DeleteCollection, analogous to genericregistry.Store's
	// PredicateFunc field.
	PredicateFunc func(label labels.Selector, field fields.Selector) storage.SelectionPredicate

	// DefaultQualifiedResource and SingularQualifiedResource are this
	// resource's plural/singular GroupResource, used for error messages,
	// GetSingularName, and RESTOptionsGetter.GetRESTOptions.
	DefaultQualifiedResource  schema.GroupResource
	SingularQualifiedResource schema.GroupResource

	// Strategy carries the resource's create/update validation, defaulting,
	// and scope behavior -- see the Strategy interface above.
	Strategy Strategy

	// ResetFieldsStrategy is optional (nil by default, matching every
	// resource strategy in this repo today, none of which implements
	// rest.ResetFieldsStrategy) -- see GetResetFields, which reproduces
	// genericregistry.Store.GetResetFields (vendor store.go:1698-1704)
	// exactly, including its nil-when-unset behavior.
	ResetFieldsStrategy rest.ResetFieldsStrategy

	// TableConvertor renders ConvertToTable responses. If left nil,
	// NewStore defaults it to rest.NewDefaultTableConvertor(DefaultQualifiedResource)
	// (name + age columns only), matching every resource's current
	// // TODO: define table converter ... in its etcd.go.
	TableConvertor rest.TableConvertor

	// Storage is the shared storage.Interface (this repo's StorageImpl)
	// this Store reads/writes through. Set by NewStore.
	Storage storage.Interface

	// keyPrefix is this resource's on-disk key prefix, derived by NewStore
	// from optsGetter.GetRESTOptions exactly the way
	// genericregistry.Store.CompleteWithOptions does (vendor
	// store.go:1553-1567), so both an OLD genericregistry.Store-based
	// NewREST and this Store address identical on-disk keys for the same
	// resource.
	keyPrefix string
}

var (
	_ rest.StandardStorage      = &Store{}
	_ rest.Scoper               = &Store{}
	_ rest.SingularNameProvider = &Store{}
	_ rest.TableConvertor       = &Store{}
	_ rest.CollectionDeleter    = &Store{}
	_ rest.ResetFieldsStrategy  = &Store{}
)

// NewStore completes a partially-configured Store (NewFunc, NewListFunc,
// PredicateFunc, DefaultQualifiedResource, SingularQualifiedResource,
// Strategy, and optionally ResetFieldsStrategy/TableConvertor set by the
// caller) by deriving its on-disk key prefix from optsGetter, the same
// RESTOptionsGetter the OLD genericregistry.Store-based NewREST for the same
// resource is wired with (see pkg/apiserver/apiserver.go's `ep` helper), and
// wiring in storageImpl. It returns a new, independent *Store -- the cfg
// value passed in is not mutated.
func NewStore(storageImpl storage.Interface, optsGetter generic.RESTOptionsGetter, cfg Store) (*Store, error) {
	if cfg.NewFunc == nil || cfg.NewListFunc == nil {
		return nil, fmt.Errorf("genericrest.NewStore: NewFunc and NewListFunc are required")
	}
	if cfg.Strategy == nil {
		return nil, fmt.Errorf("genericrest.NewStore: Strategy is required")
	}
	if cfg.PredicateFunc == nil {
		return nil, fmt.Errorf("genericrest.NewStore: PredicateFunc is required")
	}
	if cfg.DefaultQualifiedResource.Empty() {
		return nil, fmt.Errorf("genericrest.NewStore: DefaultQualifiedResource is required")
	}
	if cfg.SingularQualifiedResource.Empty() {
		// A missing one would otherwise fail silently: GetSingularName would
		// return "" and API discovery's singular column / singular-name
		// resolution (e.g. `kubectl api-resources`) would just be blank
		// rather than surfacing a construction-time error.
		return nil, fmt.Errorf("genericrest.NewStore: SingularQualifiedResource is required")
	}
	if cfg.TableConvertor == nil {
		// preserved verbatim from every resource's etcd.go NewREST -- not
		// addressed by this migration.
		cfg.TableConvertor = rest.NewDefaultTableConvertor(cfg.DefaultQualifiedResource)
	}
	cfg.Storage = storageImpl

	// Derive the key prefix exactly the way genericregistry.Store.CompleteWithOptions
	// does (vendor store.go:1553-1567), by calling the same RESTOptionsGetter the
	// OLD NewREST is wired with -- so both implementations address identical
	// on-disk keys for the same resource.
	opts, err := optsGetter.GetRESTOptions(cfg.DefaultQualifiedResource, cfg.NewFunc())
	if err != nil {
		return nil, err
	}
	prefix := opts.ResourcePrefix
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	if prefix == "/" {
		return nil, fmt.Errorf("store for %s has an invalid prefix %q", cfg.DefaultQualifiedResource.String(), opts.ResourcePrefix)
	}
	cfg.keyPrefix = prefix

	store := cfg
	return &store, nil
}

// KeyFunc chooses between genericregistry.NamespaceKeyFunc and
// genericregistry.NoNamespaceKeyFunc based on r.Strategy.NamespaceScoped(),
// exactly the same conditional genericregistry.Store.CompleteWithOptions
// uses to pick a KeyFunc (vendor store.go:1514-1586) -- reproduced by
// calling the same exported vendor functions directly rather than
// re-deriving their path.IsValidPathSegmentName guards.
func (r *Store) KeyFunc(ctx context.Context, name string) (string, error) {
	if r.Strategy.NamespaceScoped() {
		return genericregistry.NamespaceKeyFunc(ctx, r.keyPrefix, name)
	}
	return genericregistry.NoNamespaceKeyFunc(ctx, r.keyPrefix, name)
}

// KeyRootFunc mirrors genericregistry.Store's KeyRootFunc field the same
// way: for a namespaced resource it's the prefix plus the request's
// namespace (when one is present in ctx); for a cluster-scoped resource
// it's the bare prefix (vendor store.go:1569-1586).
func (r *Store) KeyRootFunc(ctx context.Context) string {
	if r.Strategy.NamespaceScoped() {
		return genericregistry.NamespaceKeyRootFunc(ctx, r.keyPrefix)
	}
	return r.keyPrefix
}

// --- rest.Storage / rest.Scoper / rest.SingularNameProvider ---

func (r *Store) New() runtime.Object {
	return r.NewFunc()
}

func (r *Store) NewList() runtime.Object {
	return r.NewListFunc()
}

// Destroy is a deliberate no-op: Store does not own the lifecycle of the
// shared StorageImpl/pool passed in by apiserver.go (it is shared with the
// old NewREST-based implementation and other resources), so there is
// nothing for this specific rest.Storage to clean up on shutdown.
func (r *Store) Destroy() {}

func (r *Store) NamespaceScoped() bool {
	return r.Strategy.NamespaceScoped()
}

func (r *Store) GetSingularName() string {
	return r.SingularQualifiedResource.Resource
}

// GetResetFields implements rest.ResetFieldsStrategy, reproducing
// genericregistry.Store.GetResetFields (vendor store.go:1698-1704) exactly:
// nil whenever no ResetFieldsStrategy is configured, which is every
// resource strategy in this repo today.
func (r *Store) GetResetFields() map[fieldpath.APIVersion]*fieldpath.Set {
	if r.ResetFieldsStrategy == nil {
		return nil
	}
	return r.ResetFieldsStrategy.GetResetFields()
}

func (r *Store) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1.Table, error) {
	return r.TableConvertor.ConvertToTable(ctx, object, tableOptions)
}

// --- Get / List / Watch ---

func (r *Store) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	obj := r.New()
	key, err := r.KeyFunc(ctx, name)
	if err != nil {
		return nil, err
	}
	rv := ""
	if options != nil {
		rv = options.ResourceVersion
	}
	if err := r.Storage.Get(ctx, key, storage.GetOptions{ResourceVersion: rv}, obj); err != nil {
		return nil, storeerr.InterpretGetError(err, r.DefaultQualifiedResource, name)
	}
	return obj, nil
}

// narrowToSingleNamespace mirrors vendor ListPredicate/WatchPredicate's
// namespace-narrowing optimization (vendor store.go:405-412, 1440-1447): if
// the request isn't already namespace-scoped but the field selector matches
// exactly one (valid) namespace, inject that namespace into ctx so
// KeyFunc/KeyRootFunc can narrow the underlying storage query to that single
// namespace's key prefix instead of scanning every namespace. This is a
// perf/fidelity optimization only -- listMetadata's own namespace filtering
// already returns correct results either way.
func narrowToSingleNamespace(ctx context.Context, p storage.SelectionPredicate) context.Context {
	if requestNamespace, _ := genericapirequest.NamespaceFrom(ctx); len(requestNamespace) != 0 {
		return ctx
	}
	selectorNamespace, ok := p.MatchesSingleNamespace()
	if !ok || len(validation.ValidateNamespaceName(selectorNamespace, false)) != 0 {
		return ctx
	}
	return genericapirequest.WithNamespace(ctx, selectorNamespace)
}

// List propagates Limit/Continue/ResourceVersionMatch (checklist item 12),
// mirroring genericregistry.Store.ListPredicate (vendor store.go:389-425).
func (r *Store) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	label := labels.Everything()
	if options != nil && options.LabelSelector != nil {
		label = options.LabelSelector
	}
	fld := fields.Everything()
	if options != nil && options.FieldSelector != nil {
		fld = options.FieldSelector
	}
	p := r.PredicateFunc(label, fld)

	list := r.NewList()
	storageOpts := storage.ListOptions{Predicate: p, Recursive: true}
	if options != nil {
		p.Limit = options.Limit
		p.Continue = options.Continue
		storageOpts.Predicate = p
		storageOpts.ResourceVersion = options.ResourceVersion
		storageOpts.ResourceVersionMatch = options.ResourceVersionMatch
	}

	ctx = narrowToSingleNamespace(ctx, p)
	key := r.KeyRootFunc(ctx)
	if name, ok := p.MatchesSingle(); ok {
		if k, err := r.KeyFunc(ctx, name); err == nil {
			key = k
			storageOpts.Recursive = false
		}
	}

	if err := r.Storage.GetList(ctx, key, storageOpts, list); err != nil {
		return nil, storeerr.InterpretListError(err, r.DefaultQualifiedResource)
	}
	return list, nil
}

// Watch propagates AllowWatchBookmarks/SendInitialEvents (checklist item
// 13), mirroring genericregistry.Store.Watch/WatchPredicate (vendor
// store.go:1417-1463). Whether the underlying StorageImpl.Watch actually
// honors these fields is unchanged by this migration either way -- both old
// and new implementations pass through to the exact same StorageImpl.Watch.
func (r *Store) Watch(ctx context.Context, options *metainternalversion.ListOptions) (watch.Interface, error) {
	label := labels.Everything()
	if options != nil && options.LabelSelector != nil {
		label = options.LabelSelector
	}
	fld := fields.Everything()
	if options != nil && options.FieldSelector != nil {
		fld = options.FieldSelector
	}
	p := r.PredicateFunc(label, fld)

	resourceVersion := ""
	var sendInitialEvents *bool
	if options != nil {
		resourceVersion = options.ResourceVersion
		p.AllowWatchBookmarks = options.AllowWatchBookmarks
		sendInitialEvents = options.SendInitialEvents
	}

	storageOpts := storage.ListOptions{ResourceVersion: resourceVersion, Predicate: p, Recursive: true, SendInitialEvents: sendInitialEvents}
	ctx = narrowToSingleNamespace(ctx, p)
	key := r.KeyRootFunc(ctx)
	if name, ok := p.MatchesSingle(); ok {
		if k, err := r.KeyFunc(ctx, name); err == nil {
			key = k
			storageOpts.Recursive = false
		}
	}
	return r.Storage.Watch(ctx, key, storageOpts)
}

// --- Create ---

// Create implements checklist items 1 (UID/creationTimestamp stamping), 2
// (generateName + bounded retry), and 7 (dry-run).
func (r *Store) Create(ctx context.Context, obj runtime.Object, createValidation rest.ValidateObjectFunc, options *metav1.CreateOptions) (runtime.Object, error) {
	if objectMeta, err := meta.Accessor(obj); err == nil && needsNameGeneration(objectMeta) {
		return r.createWithGenerateNameRetry(ctx, obj, createValidation, options)
	}
	return r.create(ctx, obj, createValidation, options)
}

func needsNameGeneration(objectMeta metav1.Object) bool {
	return len(objectMeta.GetGenerateName()) > 0 && len(objectMeta.GetName()) == 0
}

// createWithGenerateNameRetry mirrors vendor store.go:467-475 exactly
// (checklist item 2): up to maxNameGenerationCreateAttempts, each with a
// freshly generated name.
func (r *Store) createWithGenerateNameRetry(ctx context.Context, obj runtime.Object, createValidation rest.ValidateObjectFunc, options *metav1.CreateOptions) (resultObj runtime.Object, err error) {
	for i := 0; i < maxNameGenerationCreateAttempts; i++ {
		resultObj, err = r.create(ctx, obj.DeepCopyObject(), createValidation, options)
		if err == nil || !apierrors.IsAlreadyExists(err) {
			return resultObj, err
		}
	}
	return resultObj, err
}

func (r *Store) create(ctx context.Context, obj runtime.Object, createValidation rest.ValidateObjectFunc, options *metav1.CreateOptions) (runtime.Object, error) {
	objectMeta, err := meta.Accessor(obj)
	if err != nil {
		return nil, err
	}

	// checklist item 1: UID + creationTimestamp stamping, mirroring
	// rest.FillObjectMetaSystemFields (vendor meta.go:39-42), invoked at the
	// same point genericregistry.Store.create does (vendor store.go:484).
	rest.FillObjectMetaSystemFields(objectMeta)
	if needsNameGeneration(objectMeta) {
		objectMeta.SetName(r.Strategy.GenerateName(objectMeta.GetGenerateName()))
	}

	// rest.BeforeCreate invokes r.Strategy.Validate (via RESTCreateStrategy),
	// mirroring exactly where/how genericregistry.Store's own create() calls
	// it (vendor store.go, via the same rest.BeforeCreate helper) -- a
	// Validate failure here produces apierrors.NewInvalid, identically to
	// the OLD implementation.
	if err := rest.BeforeCreate(r.Strategy, ctx, obj); err != nil {
		return nil, err
	}
	if createValidation != nil {
		if err := createValidation(ctx, obj.DeepCopyObject()); err != nil {
			return nil, err
		}
	}

	name := objectMeta.GetName()
	key, err := r.KeyFunc(ctx, name)
	if err != nil {
		return nil, err
	}

	out := r.New()

	// checklist item 7: dry-run. Rather than depending on
	// genericregistry.DryRunnableStorage.Create (vendor dryrun.go:39-47),
	// which round-trips through a runtime.Codec, this uses a plain
	// DeepCopyObject preview instead -- see deepCopyInto below.
	if dryrun.IsDryRun(options.DryRun) {
		if err := r.Storage.Get(ctx, key, storage.GetOptions{}, out); err == nil {
			// Mirrors the real (non-dry-run) path below: an exhausted
			// generateName retry loop must surface as a retryable
			// GenerateNameConflict, not a plain AlreadyExists, whether or
			// not dry-run is set.
			existsErr := storeerr.InterpretCreateError(storage.NewKeyExistsError(key, 0), r.DefaultQualifiedResource, name)
			return nil, rest.CheckGeneratedNameError(ctx, r.Strategy, existsErr, obj)
		}
		if err := deepCopyInto(obj, out); err != nil {
			return nil, err
		}
		return out, nil
	}

	if err := r.Storage.Create(ctx, key, obj, out, 0); err != nil {
		err = storeerr.InterpretCreateError(err, r.DefaultQualifiedResource, name)
		err = rest.CheckGeneratedNameError(ctx, r.Strategy, err, obj)
		if !apierrors.IsAlreadyExists(err) {
			return nil, err
		}
		if errGet := r.Storage.Get(ctx, key, storage.GetOptions{}, out); errGet != nil {
			return nil, err
		}
		if accessor, errAcc := meta.Accessor(out); errAcc == nil && accessor.GetDeletionTimestamp() != nil {
			if statusErr, ok := err.(*apierrors.StatusError); ok {
				statusErr.ErrStatus.Message = fmt.Sprintf("object is being deleted: %s", statusErr.ErrStatus.Message)
			}
		}
		return nil, err
	}
	return out, nil
}

// deepCopyInto copies src into the pointed-to value of dst via
// runtime.Object.DeepCopyObject, avoiding any dependency on a
// runtime.Codec (see the dry-run Create comment above for why).
func deepCopyInto(src, dst runtime.Object) error {
	dv := reflect.ValueOf(dst)
	if dv.Kind() != reflect.Ptr || dv.IsNil() {
		return fmt.Errorf("deepCopyInto: dst must be a non-nil pointer")
	}
	copied := src.DeepCopyObject()
	cv := reflect.ValueOf(copied)
	if cv.Kind() == reflect.Ptr {
		cv = cv.Elem()
	}
	if !cv.Type().AssignableTo(dv.Elem().Type()) {
		return fmt.Errorf("deepCopyInto: cannot assign %s into %s", cv.Type(), dv.Elem().Type())
	}
	dv.Elem().Set(cv)
	return nil
}

// --- Update ---

// Update implements checklist items 3 (BeforeUpdate carry-over rules), 4
// (resourceVersion conflict -> 409), 5 (re-invocation of update
// transformation/admission on retry), and 7 (dry-run).
//
// Finding for checklist item 5: the tryUpdate closure is passed by
// reference into storage.Interface.GuaranteedUpdate exactly once; it does
// NOT need a separate retry loop here, because StorageImpl.GuaranteedUpdate's
// own retry loop already re-invokes the *same* closure -- including
// objInfo.UpdatedObject (the admission/transformation step) and
// createValidation/updateValidation -- on every precondition-check-failed or
// stale-tryUpdate-error retry.
func (r *Store) Update(ctx context.Context, name string, objInfo rest.UpdatedObjectInfo, createValidation rest.ValidateObjectFunc, updateValidation rest.ValidateObjectUpdateFunc, forceAllowCreate bool, options *metav1.UpdateOptions) (runtime.Object, bool, error) {
	key, err := r.KeyFunc(ctx, name)
	if err != nil {
		return nil, false, err
	}

	storagePreconditions := &storage.Preconditions{}
	if preconditions := objInfo.Preconditions(); preconditions != nil {
		storagePreconditions.UID = preconditions.UID
		storagePreconditions.ResourceVersion = preconditions.ResourceVersion
	}

	ignoreNotFound := r.Strategy.AllowCreateOnUpdate() || forceAllowCreate

	var (
		creating    bool
		creatingObj runtime.Object
		deleteObj   runtime.Object
	)

	tryUpdate := func(existing runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
		existingResourceVersion, err := r.Storage.Versioner().ObjectResourceVersion(existing)
		if err != nil {
			return nil, nil, err
		}
		if existingResourceVersion == 0 && !r.Strategy.AllowCreateOnUpdate() && !forceAllowCreate {
			return nil, nil, apierrors.NewNotFound(r.DefaultQualifiedResource, name)
		}

		obj, err := objInfo.UpdatedObject(ctx, existing)
		if err != nil {
			return nil, nil, err
		}

		newResourceVersion, err := r.Storage.Versioner().ObjectResourceVersion(obj)
		if err != nil {
			return nil, nil, err
		}
		doUnconditionalUpdate := newResourceVersion == 0 && r.Strategy.AllowUnconditionalUpdate()

		if existingResourceVersion == 0 {
			objectMeta, err := meta.Accessor(obj)
			if err != nil {
				return nil, nil, err
			}
			rest.FillObjectMetaSystemFields(objectMeta)
			creating = true
			creatingObj = obj
			if err := rest.BeforeCreate(r.Strategy, ctx, obj); err != nil {
				return nil, nil, err
			}
			if createValidation != nil {
				if err := createValidation(ctx, obj.DeepCopyObject()); err != nil {
					return nil, nil, err
				}
			}
			return obj, nil, nil
		}

		creating = false
		creatingObj = nil
		if doUnconditionalUpdate {
			if err := r.Storage.Versioner().UpdateObject(obj, res.ResourceVersion); err != nil {
				return nil, nil, err
			}
		} else {
			// checklist item 4: resourceVersion conflict -> 409, mirroring
			// vendor store.go:724-738 exactly, including the "must be
			// specified" Invalid case.
			if newResourceVersion == 0 {
				qualifiedKind := schema.GroupKind{Group: r.DefaultQualifiedResource.Group, Kind: r.DefaultQualifiedResource.Resource}
				fieldErrList := field.ErrorList{field.Invalid(field.NewPath("metadata").Child("resourceVersion"), newResourceVersion, "must be specified for an update")}
				return nil, nil, apierrors.NewInvalid(qualifiedKind, name, fieldErrList)
			}
			if newResourceVersion != existingResourceVersion {
				return nil, nil, apierrors.NewConflict(r.DefaultQualifiedResource, name, errors.New(OptimisticLockErrorMsg))
			}
		}

		// checklist item 3: the five BeforeUpdate carry-over rules (generation
		// freeze; UID/creationTimestamp/deletionTimestamp/deletionGracePeriodSeconds
		// carry-over), and -- since Strategy satisfies RESTUpdateStrategy --
		// this is also where r.Strategy.ValidateUpdate is invoked, mirroring
		// exactly where genericregistry.Store's own update path calls it.
		if err := rest.BeforeUpdate(r.Strategy, ctx, obj, existing); err != nil {
			return nil, nil, err
		}

		if updateValidation != nil {
			if err := updateValidation(ctx, obj.DeepCopyObject(), existing.DeepCopyObject()); err != nil {
				return nil, nil, err
			}
		}

		if genericregistry.ShouldDeleteDuringUpdate(ctx, key, obj, existing) {
			deleteObj = obj
			return nil, nil, errEmptiedFinalizers
		}

		return obj, nil, nil
	}

	out := r.New()

	if dryrun.IsDryRun(options.DryRun) {
		// mirrors genericregistry.DryRunnableStorage.GuaranteedUpdate's
		// dry-run branch (vendor dryrun.go:77-107): call tryUpdate exactly
		// once against the current state, do not persist.
		current := r.New()
		if err := r.Storage.Get(ctx, key, storage.GetOptions{IgnoreNotFound: ignoreNotFound}, current); err != nil {
			return nil, false, storeerr.InterpretUpdateError(err, r.DefaultQualifiedResource, name)
		}
		if err := storagePreconditions.Check(key, current); err != nil {
			return nil, false, storeerr.InterpretUpdateError(err, r.DefaultQualifiedResource, name)
		}
		rv, err := r.Storage.Versioner().ObjectResourceVersion(current)
		if err != nil {
			return nil, false, err
		}
		updated, _, err := tryUpdate(current, storage.ResponseMeta{ResourceVersion: rv})
		if err != nil {
			if errors.Is(err, errEmptiedFinalizers) {
				return deleteObj, false, nil
			}
			return nil, false, interpretTryUpdateError(ctx, err, creating, r.DefaultQualifiedResource, name, r.Strategy, creatingObj)
		}
		if err := deepCopyInto(updated, out); err != nil {
			return nil, false, err
		}
		return out, creating, nil
	}

	err = r.Storage.GuaranteedUpdate(ctx, key, out, ignoreNotFound, storagePreconditions, tryUpdate, nil)
	if err != nil {
		if errors.Is(err, errEmptiedFinalizers) {
			return r.deleteWithoutFinalizers(ctx, name, key, deleteObj, storagePreconditions)
		}
		return nil, false, interpretTryUpdateError(ctx, err, creating, r.DefaultQualifiedResource, name, r.Strategy, creatingObj)
	}
	return out, creating, nil
}

func interpretTryUpdateError(ctx context.Context, err error, creating bool, qualifiedResource schema.GroupResource, name string, strategy rest.RESTCreateStrategy, creatingObj runtime.Object) error {
	if creating {
		err = storeerr.InterpretCreateError(err, qualifiedResource, name)
		return rest.CheckGeneratedNameError(ctx, strategy, err, creatingObj)
	}
	return storeerr.InterpretUpdateError(err, qualifiedResource, name)
}

// deleteWithoutFinalizers mirrors vendor store.go:590-612: it hard-deletes
// an object whose finalizers were just emptied by an Update, tolerating a
// racy concurrent delete (NotFound).
func (r *Store) deleteWithoutFinalizers(ctx context.Context, name, key string, obj runtime.Object, preconditions *storage.Preconditions) (runtime.Object, bool, error) {
	out := r.New()
	if err := r.Storage.Delete(ctx, key, out, preconditions, storage.ValidateObjectFunc(rest.ValidateAllObjectFunc), nil, storage.DeleteOptions{}); err != nil {
		if storage.IsNotFound(err) {
			return obj, false, nil
		}
		return nil, false, storeerr.InterpretDeleteError(err, r.DefaultQualifiedResource, name)
	}
	return obj, false, nil
}

// --- Delete / DeleteCollection ---

// Delete implements checklist items 6 (finalizer/graceful-deletion state
// machine) and 7 (dry-run). See knownservers/custom_rest.go's original
// comment (preserved in git history) for the full reasoning this mirrors --
// graceful deletion and GC-finalizer injection never trigger for any
// resource in this repo (no RESTGracefulDeleteStrategy implementations,
// EnableGarbageCollection=false everywhere), so what remains is: an object
// with existing finalizers gets deletionTimestamp/deletionGracePeriodSeconds
// set via GuaranteedUpdate rather than hard-deleted; hard delete happens
// later via a subsequent Update that empties the finalizers list (handled by
// deleteWithoutFinalizers) or immediately here if there were no finalizers.
func (r *Store) Delete(ctx context.Context, name string, deleteValidation rest.ValidateObjectFunc, options *metav1.DeleteOptions) (runtime.Object, bool, error) {
	key, err := r.KeyFunc(ctx, name)
	if err != nil {
		return nil, false, err
	}

	obj := r.New()
	if err := r.Storage.Get(ctx, key, storage.GetOptions{}, obj); err != nil {
		return nil, false, storeerr.InterpretDeleteError(err, r.DefaultQualifiedResource, name)
	}

	if options == nil {
		options = metav1.NewDeleteOptions(0)
	}
	var preconditions storage.Preconditions
	if options.Preconditions != nil {
		preconditions.UID = options.Preconditions.UID
		preconditions.ResourceVersion = options.Preconditions.ResourceVersion
	}

	graceful, pendingGraceful, err := rest.BeforeDelete(r.Strategy, ctx, obj, options)
	if err != nil {
		return nil, false, err
	}
	if pendingGraceful {
		// Unreachable for any current resource (graceful is always false, so
		// pendingGraceful can never be set either), kept for structural
		// completeness/future-proofing per BeforeDelete's contract.
		return obj, false, nil
	}

	accessor, err := meta.Accessor(obj)
	if err != nil {
		return nil, false, apierrors.NewInternalError(err)
	}
	pendingFinalizers := len(accessor.GetFinalizers()) != 0
	dryRun := dryrun.IsDryRun(options.DryRun)

	if graceful || pendingFinalizers {
		if dryRun {
			// Mirrors vendor DryRunnableStorage.Delete's dry-run branch (vendor
			// registry/dryrun.go:49-58): dry-run must still run deleteValidation
			// against the current object -- skipping it would let a validating
			// webhook that denies the delete pass silently under --dry-run=server.
			if deleteValidation != nil {
				if err := deleteValidation(ctx, obj); err != nil {
					return nil, false, err
				}
			}
			preview := obj.DeepCopyObject()
			previewAccessor, err := meta.Accessor(preview)
			if err != nil {
				return nil, false, apierrors.NewInternalError(err)
			}
			markAsDeleting(previewAccessor)
			return preview, false, nil
		}

		out := r.New()
		updateErr := r.Storage.GuaranteedUpdate(ctx, key, out, false, &preconditions,
			func(existing runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
				if deleteValidation != nil {
					if err := deleteValidation(ctx, existing); err != nil {
						return nil, nil, err
					}
				}
				existingAccessor, err := meta.Accessor(existing)
				if err != nil {
					return nil, nil, err
				}
				if len(existingAccessor.GetFinalizers()) == 0 {
					// Finalizers were cleared concurrently; fall through to a hard delete below.
					return nil, nil, errDeleteNow
				}
				// Mutate a copy, not `existing` in place, and return the
				// copy -- see custom_rest.go's original finding (preserved
				// in git history) on why StorageImpl.GuaranteedUpdateWithConn's
				// no-op-update detection requires this.
				updated := existing.DeepCopyObject()
				updatedAccessor, err := meta.Accessor(updated)
				if err != nil {
					return nil, nil, err
				}
				markAsDeleting(updatedAccessor)
				return updated, nil, nil
			}, nil)
		switch {
		case updateErr == nil:
			return out, false, nil
		case errors.Is(updateErr, errDeleteNow):
			// fall through to hard delete below
		default:
			return nil, false, storeerr.InterpretDeleteError(updateErr, r.DefaultQualifiedResource, name)
		}
	}

	// Immediate hard delete. Mirrors genericregistry.Store.Delete's final
	// finalizeDelete call (vendor store.go:1218, dry-run included -- vendor
	// store.go:1200-1202 only short-circuits early when a graceful/finalizer
	// update already produced a non-nil `out`, which never happens on this
	// no-finalizer path): since no resource in this repo sets
	// ReturnDeletedObject/DeleteReturnsDeletedObject, the result is a
	// *metav1.Status, not the deleted object itself, for BOTH dry-run and
	// real hard deletes.
	if dryRun {
		// Mirrors vendor DryRunnableStorage.Delete's dry-run branch (vendor
		// registry/dryrun.go:49-58): still run deleteValidation against the
		// current object before returning, for the same reason as the
		// graceful/pendingFinalizers dry-run branch above.
		if deleteValidation != nil {
			if err := deleteValidation(ctx, obj); err != nil {
				return nil, false, err
			}
		}
		return r.finalizeDeleteStatus(obj)
	}
	out := r.New()
	if err := r.Storage.Delete(ctx, key, out, &preconditions, storage.ValidateObjectFunc(deleteValidationOrAll(deleteValidation)), nil, storage.DeleteOptions{}); err != nil {
		return nil, false, storeerr.InterpretDeleteError(err, r.DefaultQualifiedResource, name)
	}
	return r.finalizeDeleteStatus(out)
}

// finalizeDeleteStatus mirrors genericregistry.Store.finalizeDelete's
// non-ReturnDeletedObject branch (vendor store.go:1397-1409) exactly,
// including setting the Kind field to the plural resource name (sic --
// preserved verbatim, not "fixed", to match existing behavior).
func (r *Store) finalizeDeleteStatus(obj runtime.Object) (runtime.Object, bool, error) {
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return nil, false, apierrors.NewInternalError(err)
	}
	details := &metav1.StatusDetails{
		Name:  accessor.GetName(),
		Group: r.DefaultQualifiedResource.Group,
		Kind:  r.DefaultQualifiedResource.Resource, // Yes we set Kind field to resource -- see vendor comment.
		UID:   accessor.GetUID(),
	}
	return &metav1.Status{Status: metav1.StatusSuccess, Details: details}, true, nil
}

func deleteValidationOrAll(deleteValidation rest.ValidateObjectFunc) rest.ValidateObjectFunc {
	if deleteValidation != nil {
		return deleteValidation
	}
	return rest.ValidateAllObjectFunc
}

// markAsDeleting mirrors vendor store.go:1011-1029 (markAsDeleting): bumps
// generation (if the resource uses generations and isn't already being
// deleted), sets deletionTimestamp to now (if unset or later than now), and
// forces deletionGracePeriodSeconds to 0 (immediate, since no resource in
// this repo supports graceful deletion -- see the Delete comment above).
func markAsDeleting(accessor metav1.Object) {
	if accessor.GetDeletionTimestamp() == nil && accessor.GetGeneration() > 0 {
		accessor.SetGeneration(accessor.GetGeneration() + 1)
	}
	now := metav1.Now()
	if existing := accessor.GetDeletionTimestamp(); existing == nil || existing.After(now.Time) {
		accessor.SetDeletionTimestamp(&now)
	}
	var zero int64
	accessor.SetDeletionGracePeriodSeconds(&zero)
}

// DefaultDeleteCollectionPageSize bounds how many items DeleteCollection
// deletes per internal List call when the caller didn't request a specific
// page size. This is deliberately DeleteCollection's own constant, not
// derived from List's behavior: List (StorageImpl.GetList) used to impose an
// implicit page size of 500 on any unpaginated call, and DeleteCollection's
// pagination loop below was silently relying on that -- its only
// cancellation checkpoint is the ctx.Done() check at the top of the loop,
// which used to run roughly every 500 deleted items as a side effect of
// List's old default. Once List was fixed to return everything in one call
// when no Limit is given (see docs/features/getlist-unset-limit-returns-everything.md),
// that implicit checkpoint disappeared: a "delete all" call with no explicit
// Limit would list and then delete an entire large collection in one
// uninterruptible pass. DeleteCollection now requests its own bounded page
// explicitly, independent of whatever List's own default happens to be.
const DefaultDeleteCollectionPageSize = 500

// DeleteCollection implements checklist item 10. Unlike
// genericregistry.Store.DeleteCollection (vendor store.go:1237-1382), this
// is a simple sequential implementation (no worker pool) -- every resource
// using this package so far is low-traffic enough that a bulk delete is not
// expected to need parallelism. It does still paginate through List's
// continue token like vendor does (vendor store.go:1298-1362): silently
// stopping at one page would delete only part of the collection while
// reporting success, and pagination is also what keeps the ctx.Done() check
// below effective on a large collection (see DefaultDeleteCollectionPageSize).
// As in vendor, a caller-supplied explicit Limit is honored as a request for
// just that one page rather than paginated through. Every matched object is
// still individually deleted through the same Delete path above (so
// finalizers/dry-run/etc. behave identically per-item), and the returned
// list mirrors everything that was deleted across all pages.
func (r *Store) DeleteCollection(ctx context.Context, deleteValidation rest.ValidateObjectFunc, options *metav1.DeleteOptions, listOptions *metainternalversion.ListOptions) (runtime.Object, error) {
	if listOptions == nil {
		listOptions = &metainternalversion.ListOptions{}
	} else {
		listOptions = listOptions.DeepCopy()
	}
	hasLimit := listOptions.Limit > 0
	if !hasLimit {
		// Force our own bounded page size rather than relying on List's
		// default (which, correctly, no longer imposes one on its own) --
		// see DefaultDeleteCollectionPageSize.
		listOptions.Limit = DefaultDeleteCollectionPageSize
	}

	var deleted []runtime.Object
	var originalList runtime.Object
	for {
		// Mirrors vendor store.go's own ctx.Done() check at the top of its
		// DeleteCollection pagination loop: without it, a client disconnecting
		// mid-delete on a large collection would run the delete to full
		// exhaustion instead of stopping early.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		listObj, err := r.List(ctx, listOptions)
		if err != nil {
			return nil, err
		}
		items, err := meta.ExtractList(listObj)
		if err != nil {
			return nil, err
		}

		for _, item := range items {
			accessor, err := meta.Accessor(item)
			if err != nil {
				return nil, err
			}
			var perItemOptions *metav1.DeleteOptions
			if options != nil {
				perItemOptions = options.DeepCopy()
			}
			if _, _, err := r.Delete(ctx, accessor.GetName(), deleteValidation, perItemOptions); err != nil && !apierrors.IsNotFound(err) {
				return nil, err
			}
			deleted = append(deleted, item)
		}

		if hasLimit {
			// The original request was itself asking for a single page; honor
			// just that page rather than continuing on, mirroring vendor.
			if err := meta.SetList(listObj, deleted); err != nil {
				return nil, err
			}
			return listObj, nil
		}

		if originalList == nil {
			originalList = listObj
			if err := meta.SetList(originalList, nil); err != nil {
				return nil, err
			}
		}

		listAccessor, err := meta.ListAccessor(listObj)
		if err != nil {
			return nil, err
		}
		if listAccessor.GetContinue() == "" {
			if err := meta.SetList(originalList, deleted); err != nil {
				return nil, err
			}
			return originalList, nil
		}

		listOptions.Continue = listAccessor.GetContinue()
		listOptions.ResourceVersion = ""
		listOptions.ResourceVersionMatch = ""
	}
}
