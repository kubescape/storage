package file

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
)

// initialEvents is what a watch with sendInitialEvents ships before live events: the
// objects under the key as Added events, then the transitions from that snapshot to the
// committed state of every object a live event touched while the list ran, and then the
// initial-events-end bookmark when the client allows bookmarks.
type initialEvents struct {
	events   []watch.Event
	listed   map[string]listedObject
	bookmark runtime.Object
	rv       uint64

	lister    initialLister
	newObject func() runtime.Object
	key       string
	recursive bool
	readRV    string
}

// listedObject is what the snapshot holds for one object: its incarnation (the uid, since
// versions restart at every creation), its version, and the object itself.
type listedObject struct {
	uid types.UID
	rv  string
	obj runtime.Object
}

func listedKey(acc metav1.Object) string {
	return acc.GetNamespace() + "/" + acc.GetName()
}

func wantsInitialEvents(opts storage.ListOptions) bool {
	return opts.SendInitialEvents != nil && *opts.SendInitialEvents
}

// reconcile re-reads every object the buffered live events name and returns the events
// that take the client from the snapshot to what the store holds now. The buffered events
// only say which objects moved: the store publishes an object's events after releasing
// its lock, so they can arrive out of commit order and are not trusted for the state
// itself. A re-read happens after the list, so it sees every write the list could have
// straddled.
func (ie initialEvents) reconcile(ctx context.Context, buffered []watch.Event) ([]watch.Event, error) {
	type touched struct {
		namespace, name string
	}
	var order []touched
	seen := map[touched]bool{}
	for _, ev := range buffered {
		acc, err := meta.Accessor(ev.Object)
		if err != nil {
			continue
		}
		t := touched{namespace: acc.GetNamespace(), name: acc.GetName()}
		if !seen[t] {
			seen[t] = true
			order = append(order, t)
		}
	}
	var out []watch.Event
	for _, t := range order {
		current := ie.newObject()
		if err := ie.lister.Get(ctx, ie.objectKey(t.namespace, t.name), storage.GetOptions{IgnoreNotFound: true, ResourceVersion: ie.readRV}, current); err != nil {
			return nil, fmt.Errorf("re-read %s/%s after the list: %w", t.namespace, t.name, err)
		}
		acc, err := meta.Accessor(current)
		if err != nil {
			return nil, err
		}
		snap, listed := ie.listed[t.namespace+"/"+t.name]
		present := acc.GetName() != ""
		switch {
		case !present && !listed:
		case !present:
			out = append(out, watch.Event{Type: watch.Deleted, Object: snap.obj})
		case !listed:
			out = append(out, watch.Event{Type: watch.Added, Object: current})
		case acc.GetUID() != snap.uid:
			out = append(out, watch.Event{Type: watch.Deleted, Object: snap.obj}, watch.Event{Type: watch.Added, Object: current})
		case acc.GetResourceVersion() != snap.rv:
			out = append(out, watch.Event{Type: watch.Modified, Object: current})
		}
	}
	return out, nil
}

// objectKey is the storage key of one object under the watched key.
func (ie initialEvents) objectKey(namespace, name string) string {
	if !ie.recursive {
		return ie.key
	}
	_, _, _, _, rootNamespace, _ := K8sPathToKeys(ie.key)
	if rootNamespace == "" && namespace != "" {
		return ie.key + "/" + namespace + "/" + name
	}
	return ie.key + "/" + name
}

// bookmarkEvent returns the initial-events-end bookmark for the state the client holds
// after the shipped events, or false when the client did not ask for bookmarks.
func (ie initialEvents) bookmarkEvent(shipped []watch.Event) (watch.Event, bool) {
	if ie.bookmark == nil {
		return watch.Event{}, false
	}
	rv := ie.rv
	for _, ev := range shipped {
		if ev.Type == watch.Deleted {
			continue
		}
		acc, err := meta.Accessor(ev.Object)
		if err != nil {
			continue
		}
		if v, err := strconv.ParseUint(acc.GetResourceVersion(), 10, 64); err == nil && v > rv {
			rv = v
		}
	}
	acc, err := meta.Accessor(ie.bookmark)
	if err != nil {
		return watch.Event{}, false
	}
	acc.SetResourceVersion(strconv.FormatUint(rv, 10))
	return watch.Event{Type: watch.Bookmark, Object: ie.bookmark}, true
}

// errorEvent is what the watch ships when the re-read after the list fails: the client
// relists instead of finishing its initial sync on a state nobody could confirm.
func errorEvent(err error) watch.Event {
	status := apierrors.NewInternalError(err).Status()
	return watch.Event{Type: watch.Error, Object: &status}
}

// requestedMinimum returns the numeric resourceVersion a client asked the snapshot to be
// no older than; "", "0" and the sentinels carry no minimum.
func requestedMinimum(resourceVersion string) (uint64, bool) {
	rv, err := strconv.ParseUint(resourceVersion, 10, 64)
	return rv, err == nil && rv > 0
}

// initialLister is the part of storage.Interface the replay reads from.
type initialLister interface {
	Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error
	GetList(ctx context.Context, key string, opts storage.ListOptions, listObj runtime.Object) error
}

// buildInitialEvents lists the objects under key, or reads the single object at key when
// the watch is not recursive, and turns them into the events a watch list starts with.
func buildInitialEvents(ctx context.Context, lister initialLister, scheme *runtime.Scheme, key string, opts storage.ListOptions) (initialEvents, error) {
	itemObj, listObj, err := newObjectsForKey(scheme, key)
	if err != nil {
		return initialEvents{}, err
	}
	predicate, err := normalizeSelectionPredicate(opts.Predicate)
	if err != nil {
		return initialEvents{}, err
	}
	// Reads stand in for the list they replace: metadata only, unless the full spec was
	// asked for explicitly.
	readRV := softwarecomposition.ResourceVersionMetadata
	if opts.ResourceVersion == softwarecomposition.ResourceVersionFullSpec {
		readRV = ""
	}
	var items []runtime.Object
	if opts.Recursive {
		listOpts := storage.ListOptions{ResourceVersion: opts.ResourceVersion, Predicate: predicate, Recursive: true}
		listOpts.Predicate.Limit = 0
		listOpts.Predicate.Continue = ""
		if err := lister.GetList(ctx, key, listOpts, listObj); err != nil {
			return initialEvents{}, err
		}
		if items, err = meta.ExtractList(listObj); err != nil {
			return initialEvents{}, err
		}
	} else {
		obj := itemObj.DeepCopyObject()
		if err := lister.Get(ctx, key, storage.GetOptions{IgnoreNotFound: true, ResourceVersion: readRV}, obj); err != nil {
			return initialEvents{}, err
		}
		acc, err := meta.Accessor(obj)
		if err != nil {
			return initialEvents{}, err
		}
		if acc.GetName() != "" {
			matched, err := predicate.Matches(obj)
			if err != nil {
				return initialEvents{}, err
			}
			if matched {
				items = append(items, obj)
			}
		}
		if err := meta.SetList(listObj, items); err != nil {
			return initialEvents{}, err
		}
	}

	snapshotRV, _ := strconv.ParseUint(computeListResourceVersion(listObj), 10, 64)
	if minimum, ok := requestedMinimum(opts.ResourceVersion); ok && snapshotRV < minimum {
		return initialEvents{}, storage.NewTooLargeResourceVersionError(minimum, snapshotRV, 1)
	}

	ie := initialEvents{
		events:    make([]watch.Event, 0, len(items)),
		listed:    make(map[string]listedObject, len(items)),
		rv:        snapshotRV,
		lister:    lister,
		newObject: itemObj.DeepCopyObject,
		key:       key,
		recursive: opts.Recursive,
		readRV:    readRV,
	}
	for _, item := range items {
		acc, err := meta.Accessor(item)
		if err != nil {
			return initialEvents{}, err
		}
		ie.listed[listedKey(acc)] = listedObject{uid: acc.GetUID(), rv: acc.GetResourceVersion(), obj: item}
		ie.events = append(ie.events, watch.Event{Type: watch.Added, Object: item})
	}
	if predicate.AllowWatchBookmarks {
		if err := storage.AnnotateInitialEventsEndBookmark(itemObj); err != nil {
			return initialEvents{}, err
		}
		ie.bookmark = itemObj
	}
	return ie, nil
}

// newObjectsForKey returns an empty object and an empty list for the resource named in
// key, found in scheme by the kind-to-resource rule resourceFromKey applies the other way.
func newObjectsForKey(scheme *runtime.Scheme, key string) (runtime.Object, runtime.Object, error) {
	if scheme == nil {
		return nil, nil, fmt.Errorf("no scheme to resolve the type of %q", key)
	}
	_, group, _, _, _, _ := K8sPathToKeys(key)
	resource := resourceFromKey(key)
	gv := schema.GroupVersion{Group: group, Version: runtime.APIVersionInternal}
	for kind := range scheme.KnownTypes(gv) {
		if strings.HasSuffix(kind, "List") || resourceOfKind(kind) != resource {
			continue
		}
		itemObj, err := scheme.New(gv.WithKind(kind))
		if err != nil {
			return nil, nil, err
		}
		listObj, err := scheme.New(gv.WithKind(kind + "List"))
		if err != nil {
			return nil, nil, err
		}
		return itemObj, listObj, nil
	}
	return nil, nil, fmt.Errorf("no type registered for resource %q of group %q", resource, group)
}

func resourceOfKind(kind string) string {
	lower := strings.ToLower(kind)
	if strings.HasSuffix(lower, "y") {
		return lower[:len(lower)-1] + "ies"
	}
	return lower + "s"
}
