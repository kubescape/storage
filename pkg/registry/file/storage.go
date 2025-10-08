package file

import (
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/utils"
	"github.com/spf13/afero"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

const (
	GobExt                   = ".g"
	JsonExt                  = ".j"
	MetadataExt              = ".m"
	DefaultStorageRoot       = "/data"
	StorageV1Beta1ApiVersion = "spdx.softwarecomposition.kubescape.io/v1beta1"
	operationNotSupportedMsg = "operation not supported"
	SchemaVersion            = int64(1)
)

var (
	ObjectCompletedError = errors.New("object is completed")
	ObjectTooLargeError  = errors.New("object is too large")
)

type objState struct {
	obj  runtime.Object
	meta *storage.ResponseMeta
	rev  int64
	data []byte
}

// StorageImpl offers a common interface for object marshaling/unmarshaling operations and
// hides all the storage-related operations behind it.
type StorageImpl struct {
	appFs           afero.Fs
	pool            *sqlitemigration.Pool
	locks           utils.MapMutex[string]
	processor       Processor
	root            string
	scheme          *runtime.Scheme
	versioner       storage.Versioner
	watchDispatcher *WatchDispatcher
}

func (s *StorageImpl) Stats(_ context.Context) (storage.Stats, error) {
	return storage.Stats{}, fmt.Errorf("unimplemented")
}

func (s *StorageImpl) SetKeysFunc(_ storage.KeysFunc) {
	//TODO implement me
	panic("implement me")
}

func (s *StorageImpl) CompactRevision() int64 {
	//TODO implement me
	panic("implement me")
}

// StorageQuerier wraps the storage.Interface and adds some extra methods which are used by the storage implementation.
type StorageQuerier interface {
	storage.Interface
	CalculateChecksum(in runtime.Object) (string, error)
	GetByNamespace(ctx context.Context, apiVersion, kind, namespace string, listObj runtime.Object) error
	GetByCluster(ctx context.Context, apiVersion, kind string, listObj runtime.Object) error
}

var _ storage.Interface = (*StorageImpl)(nil)

var _ StorageQuerier = (*StorageImpl)(nil)

func NewStorageImpl(appFs afero.Fs, root string, pool *sqlitemigration.Pool, watchDispatcher *WatchDispatcher, scheme *runtime.Scheme) StorageQuerier {
	return NewStorageImplWithCollector(appFs, root, pool, watchDispatcher, scheme, DefaultProcessor{})
}

func NewStorageImplWithCollector(appFs afero.Fs, root string, conn *sqlitemigration.Pool, watchDispatcher *WatchDispatcher, scheme *runtime.Scheme, processor Processor) StorageQuerier {
	if watchDispatcher == nil {
		watchDispatcher = NewWatchDispatcher()
	}
	storageImpl := &StorageImpl{
		appFs:           appFs,
		pool:            conn,
		locks:           utils.NewMapMutex[string](),
		processor:       processor,
		root:            root,
		scheme:          scheme,
		versioner:       storage.APIObjectVersioner{},
		watchDispatcher: watchDispatcher,
	}
	processor.SetStorage(storageImpl)
	return storageImpl
}

func (s *StorageImpl) GetCurrentResourceVersion(_ context.Context) (uint64, error) {
	return 0, nil
}

func (s *StorageImpl) ReadinessCheck() error {
	return nil
}

// Versioner Returns Versioner associated with this interface.
func (s *StorageImpl) Versioner() storage.Versioner {
	return s.versioner
}

func extractFields(obj runtime.Object, fields []string) runtime.Object {
	val := reflect.ValueOf(obj).Elem()
	ret := reflect.New(val.Type()).Interface().(runtime.Object)
	for _, name := range fields {
		field := val.FieldByName(name)
		if field.IsValid() {
			reflect.ValueOf(ret).Elem().FieldByName(name).Set(field)
		}
	}
	return ret
}

// makePayloadPath returns a path for the payload file
func makePayloadPath(path string) string {
	return path + GobExt
}

// IsPayloadFile returns true if a given file at `path` is an object payload file, else false
func IsPayloadFile(path string) bool {
	return strings.HasSuffix(path, GobExt)
}

func (s *StorageImpl) keyFromPath(path string) string {
	extension := filepath.Ext(path)
	return strings.TrimPrefix(strings.TrimSuffix(path, extension), s.root)
}

func (s *StorageImpl) saveObject(conn *sqlite.Conn, key string, obj runtime.Object, metaOut runtime.Object, checksum string) error {
	// increment resourceVersion
	if version, err := s.versioner.ObjectResourceVersion(obj); err == nil {
		if err := s.versioner.UpdateObject(obj, version+1); err != nil {
			return fmt.Errorf("set resourceVersion: %w", err)
		}
	}
	// remove managed fields
	managedFields := reflect.ValueOf(obj).Elem().FieldByName("ObjectMeta").FieldByName("ManagedFields")
	if managedFields.IsValid() {
		managedFields.Set(reflect.Zero(managedFields.Type()))
	}
	// prepare path
	p := filepath.Join(s.root, key)
	if err := s.appFs.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	// prepare payload file
	payloadFile, err := s.appFs.OpenFile(makePayloadPath(p), syscall.O_DIRECT|os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open payload file: %w", err)
	}
	directIOWriter := NewDirectIOWriter(payloadFile)
	defer func() {
		_ = directIOWriter.Close()
		_ = payloadFile.Close()
	}()
	// write payload
	payloadEncoder := gob.NewEncoder(directIOWriter)
	if err := payloadEncoder.Encode(obj); err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}
	// extract metadata
	metadata := extractFields(obj, []string{"ObjectMeta", "SchemaVersion"})
	// calculate checksum
	if checksum == "" {
		checksum, err = s.CalculateChecksum(obj)
		if err != nil {
			return fmt.Errorf("calculate checksum: %w", err)
		}
	}
	// add checksum to metadata
	if anno := metadata.(metav1.Object).GetAnnotations(); anno == nil {
		metadata.(metav1.Object).SetAnnotations(map[string]string{helpersv1.SyncChecksumMetadataKey: checksum})
	} else {
		anno[helpersv1.SyncChecksumMetadataKey] = checksum
	}
	// store metadata in SQLite
	err = writeMetadata(conn, key, metadata)
	if err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}
	// eventually fill metaOut
	if metaOut != nil {
		val := reflect.ValueOf(metaOut)
		if val.Kind() == reflect.Ptr {
			// Dereference the pointer
			val = val.Elem()
		}
		// metadata obj into metaOut
		val.Set(reflect.ValueOf(metadata).Elem())
	}
	return nil
}

// Create adds a new object at a key unless it already exists. 'ttl' is time-to-live
// in seconds (and is ignored). If no error is returned and out is not nil, out will be
// set to the read value from database.
func (s *StorageImpl) Create(ctx context.Context, key string, obj, metaOut runtime.Object, _ uint64) error {
	conn, err := s.pool.Take(context.Background())
	if err != nil {
		return fmt.Errorf("take connection: %w", err)
	}
	defer s.pool.Put(conn)
	return s.CreateWithConn(ctx, conn, key, obj, metaOut, 0)
}

func (s *StorageImpl) CreateWithConn(ctx context.Context, conn *sqlite.Conn, key string, obj, metaOut runtime.Object, _ uint64) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.Create")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	_, spanLock := otel.Tracer("").Start(ctx, "waiting for lock")
	beforeLock := time.Now()
	err := s.locks.Lock(ctx, key)
	if err != nil {
		return apierrors.NewTimeoutError(fmt.Sprintf("lock: %v", err), 0)
	}
	defer s.locks.Unlock(key)
	spanLock.End()
	lockDuration := time.Since(beforeLock)
	if lockDuration > time.Second {
		logger.L().Debug("Create", helpers.String("key", key), helpers.String("lockDuration", lockDuration.String()))
	}
	// check if object already exists
	if _, err := s.appFs.Stat(makePayloadPath(filepath.Join(s.root, key))); err == nil {
		return storage.NewKeyExistsError(key, 0)
	}
	// resourceVersion should not be set on create
	if version, err := s.versioner.ObjectResourceVersion(obj); err == nil && version != 0 {
		msg := "resourceVersion should not be set on objects to be created"
		logger.L().Ctx(ctx).Error(msg)
		return errors.New(msg)
	}
	// call processor on object to be saved
	if err := s.processor.PreSave(ctx, conn, obj); err != nil {
		return err
	}
	// save object
	if err := s.saveObject(conn, key, obj, metaOut, ""); err != nil {
		logger.L().Ctx(ctx).Error("Create - save object failed", helpers.Error(err), helpers.String("key", key))
		return err
	}
	// call processor on saved object
	if err := s.processor.AfterCreate(ctx, conn, obj); err != nil {
		return fmt.Errorf("processor.AfterCreate: %w", err)
	}
	// publish event to watchers
	s.watchDispatcher.Added(key, metaOut, obj)
	return nil
}

// Delete removes the specified key and returns the value that existed at that spot.
// If key didn't exist, it will return NotFound storage error.
// If 'cachedExistingObject' is non-nil, it can be used as a suggestion about the
// current version of the object to avoid read operation from storage to get it.
// However, the implementations have to retry in case suggestion is stale.
func (s *StorageImpl) Delete(ctx context.Context, key string, metaOut runtime.Object, _ *storage.Preconditions, _ storage.ValidateObjectFunc, _ runtime.Object, _ storage.DeleteOptions) error {
	conn, err := s.pool.Take(context.Background())
	if err != nil {
		return fmt.Errorf("take connection: %w", err)
	}
	defer s.pool.Put(conn)
	return s.DeleteWithConn(ctx, conn, key, metaOut, nil, nil, nil, storage.DeleteOptions{})
}

func (s *StorageImpl) DeleteWithConn(ctx context.Context, conn *sqlite.Conn, key string, metaOut runtime.Object, _ *storage.Preconditions, _ storage.ValidateObjectFunc, _ runtime.Object, _ storage.DeleteOptions) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.Delete")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	_, spanLock := otel.Tracer("").Start(ctx, "waiting for lock")
	beforeLock := time.Now()
	err := s.locks.Lock(ctx, key)
	if err != nil {
		return apierrors.NewTimeoutError(fmt.Sprintf("lock: %v", err), 0)
	}
	defer s.locks.Unlock(key)
	spanLock.End()
	lockDuration := time.Since(beforeLock)
	if lockDuration > time.Second {
		logger.L().Debug("Delete", helpers.String("key", key), helpers.String("lockDuration", lockDuration.String()))
	}
	return s.delete(ctx, conn, key, metaOut, nil, nil, nil, storage.DeleteOptions{})
}

func (s *StorageImpl) delete(ctx context.Context, conn *sqlite.Conn, key string, metaOut runtime.Object, _ *storage.Preconditions, _ storage.ValidateObjectFunc, _ runtime.Object, _ storage.DeleteOptions) error {
	p := filepath.Join(s.root, key)
	// delete metadata in SQLite
	err := DeleteMetadata(conn, key, metaOut)
	if err != nil {
		logger.L().Ctx(ctx).Error("Delete - delete metadata failed", helpers.Error(err), helpers.String("key", key))
	}
	// delete payload file
	if err := s.appFs.Remove(makePayloadPath(p)); err != nil {
		logger.L().Ctx(ctx).Error("Delete - remove json file failed", helpers.Error(err), helpers.String("key", key))
	}
	// publish event to watchers
	s.watchDispatcher.Deleted(key, metaOut)
	return nil
}

// Watch begins watching the specified key. Events are decoded into API objects,
// and any items selected by 'p' are sent down to returned watch.Interface.
// resourceVersion may be used to specify what version to begin watching,
// which should be the current resourceVersion, and no longer rv+1
// (e.g. reconnecting without missing any updates).
// If resource version is "0", this interface will get current object at given key
// and send it in an "ADDED" event, before watch starts.
func (s *StorageImpl) Watch(ctx context.Context, key string, opts storage.ListOptions) (watch.Interface, error) {
	_, span := otel.Tracer("").Start(ctx, "StorageImpl.Watch")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	_, _, _, namespace, _ := pathToKeys(key)
	if namespace != "" {
		// FIXME find an alternative to fix NS deletion
		logger.L().Debug("rejecting Watch called with namespace", helpers.String("key", key), helpers.String("namespace", namespace))
		return watch.NewEmptyWatch(), nil
	}
	// TODO(ttimonen) Should we do ctx.WithoutCancel; or does the parent ctx lifetime match with expectations?
	nw := newWatcher(ctx, opts.ResourceVersion == softwarecomposition.ResourceVersionFullSpec)
	s.watchDispatcher.Register(key, nw)
	return nw, nil
}

// Get unmarshals object found at key into objPtr. On a not found error, will either
// return a zero object of the requested type, or an error, depending on 'opts.ignoreNotFound'.
// Treats empty responses and nil response nodes exactly like a not found error.
// The returned contents may be delayed, but it is guaranteed that they will
// match 'opts.ResourceVersion' according 'opts.ResourceVersionMatch'.
func (s *StorageImpl) Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	conn, err := s.pool.Take(context.Background())
	if err != nil {
		return fmt.Errorf("take connection: %w", err)
	}
	defer s.pool.Put(conn)
	return s.GetWithConn(ctx, conn, key, opts, objPtr)
}

func (s *StorageImpl) GetWithConn(ctx context.Context, conn *sqlite.Conn, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.Get")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	_, spanLock := otel.Tracer("").Start(ctx, "waiting for lock")
	beforeLock := time.Now()
	err := s.locks.RLock(ctx, key)
	if err != nil {
		return apierrors.NewTimeoutError(fmt.Sprintf("rlock: %v", err), 0)
	}
	defer s.locks.RUnlock(key)
	spanLock.End()
	lockDuration := time.Since(beforeLock)
	if lockDuration > time.Second {
		logger.L().Debug("Get", helpers.String("key", key), helpers.String("lockDuration", lockDuration.String()))
	}
	return s.get(ctx, conn, key, opts, objPtr)
}

// get is a helper function for Get to allow calls without locks from other methods that already have them
func (s *StorageImpl) get(ctx context.Context, conn *sqlite.Conn, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	p := filepath.Join(s.root, key)
	if opts.ResourceVersion == softwarecomposition.ResourceVersionMetadata {
		// get metadata from SQLite
		metadata, err := ReadMetadata(conn, key)
		if err != nil {
			if errors.Is(err, ErrMetadataNotFound) {
				if opts.IgnoreNotFound {
					return runtime.SetZeroValue(objPtr)
				} else {
					return storage.NewKeyNotFoundError(key, 0)
				}
			} else {
				return fmt.Errorf("read metadata: %w", err)
			}
		}
		return json.Unmarshal(metadata, objPtr)
	}
	payloadFile, err := s.appFs.OpenFile(makePayloadPath(p), syscall.O_DIRECT|os.O_RDONLY, 0)
	if err != nil {
		if errors.Is(err, afero.ErrFileNotFound) {
			// file not found, delete corresponding metadata
			_ = DeleteMetadata(conn, key, nil)
			if opts.IgnoreNotFound {
				return runtime.SetZeroValue(objPtr)
			} else {
				return storage.NewKeyNotFoundError(key, 0)
			}
		}
		logger.L().Ctx(ctx).Error("Get - read file failed", helpers.Error(err), helpers.String("key", key))
		return err
	}
	defer func() {
		_ = payloadFile.Close()
	}()
	decoder := gob.NewDecoder(NewDirectIOReader(payloadFile))
	err = decoder.Decode(objPtr)
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			// irrecoverable error, delete corresponding data
			_ = DeleteMetadata(conn, key, nil)
			_ = s.appFs.Remove(makePayloadPath(p))
			logger.L().Debug("Get - gob unexpected EOF, removed files", helpers.String("key", key))
			if opts.IgnoreNotFound {
				return runtime.SetZeroValue(objPtr)
			} else {
				return storage.NewKeyNotFoundError(key, 0)
			}
		}
		logger.L().Ctx(ctx).Error("Get - gob unmarshal failed", helpers.Error(err), helpers.String("key", key))
		return err
	}
	return nil
}

// GetList unmarshalls objects found at key into a *List api object (an object
// that satisfies runtime.IsList definition).
// If 'opts.Recursive' is false, 'key' is used as an exact match. If `opts.Recursive'
// is true, 'key' is used as a prefix.
// The returned contents may be delayed, but it is guaranteed that they will
// match 'opts.ResourceVersion' according 'opts.ResourceVersionMatch'.
// GetList only returns metadata for the objects, not the objects themselves.
func (s *StorageImpl) GetList(ctx context.Context, key string, opts storage.ListOptions, listObj runtime.Object) error {
	conn, err := s.pool.Take(context.Background())
	if err != nil {
		return fmt.Errorf("take connection: %w", err)
	}
	defer s.pool.Put(conn)
	return s.GetListWithConn(ctx, conn, key, opts, listObj)
}

func (s *StorageImpl) GetListWithConn(ctx context.Context, conn *sqlite.Conn, key string, opts storage.ListOptions, listObj runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.GetList")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	listPtr, err := meta.GetItemsPtr(listObj)
	if err != nil {
		logger.L().Ctx(ctx).Error("GetList - get items ptr failed", helpers.Error(err), helpers.String("key", key))
		return err
	}
	v, err := conversion.EnforcePtr(listPtr)
	if err != nil || v.Kind() != reflect.Slice {
		logger.L().Ctx(ctx).Error("GetList - need ptr to slice", helpers.Error(err), helpers.String("key", key))
		return fmt.Errorf("need ptr to slice: %v", err)
	}
	// set default limit
	if opts.Predicate.Limit == 0 {
		opts.Predicate.Limit = 500
	}
	// prepare SQLite connection
	var list []string
	var last string
	if opts.ResourceVersion == softwarecomposition.ResourceVersionFullSpec {
		// get names from SQLite
		list, last, err = listMetadataKeys(conn, key, opts.Predicate.Continue, opts.Predicate.Limit)
		if err != nil {
			logger.L().Ctx(ctx).Error("GetList - list keys failed", helpers.Error(err), helpers.String("key", key))
		}
		// populate list object
		for _, k := range list {
			elem := v.Type().Elem()
			obj := reflect.New(elem).Interface().(runtime.Object)
			if err := s.get(ctx, conn, k, storage.GetOptions{}, obj); err != nil {
				logger.L().Ctx(ctx).Error("GetList - get object failed", helpers.Error(err), helpers.String("key", k))
			}
			v.Set(reflect.Append(v, reflect.ValueOf(obj).Elem()))
		}
	} else {
		// get metadata from SQLite
		list, last, err = listMetadata(conn, key, opts.Predicate.Continue, opts.Predicate.Limit)
		if err != nil {
			logger.L().Ctx(ctx).Error("GetList - list metadata failed", helpers.Error(err), helpers.String("key", key))
		}
		// populate list object
		for _, metadataJSON := range list {
			elem := v.Type().Elem()
			obj := reflect.New(elem).Interface().(runtime.Object)
			if err := json.Unmarshal([]byte(metadataJSON), obj); err != nil {
				logger.L().Ctx(ctx).Error("GetList - unmarshal metadata failed", helpers.Error(err), helpers.String("key", key))
			}
			v.Set(reflect.Append(v, reflect.ValueOf(obj).Elem()))
		}
	}
	// eventually set list accessor fields
	if len(list) == int(opts.Predicate.Limit) {
		listAccessor, err := meta.ListAccessor(listObj)
		if err != nil {
			return fmt.Errorf("list accessor: %w", err)
		}
		listAccessor.SetContinue(last)
		//if rsp.RemainingItemCount > 0 {
		//listAccessor.SetRemainingItemCount(&rsp.RemainingItemCount)
		//}
		//if rsp.ResourceVersion > 0 {
		//listAccessor.SetResourceVersion(strconv.FormatInt(rsp.ResourceVersion, 10))
		//}
	}
	return nil
}

// getListWithSpec is the same as GetList, but it returns the full objects instead of just the metadata.
func (s *StorageImpl) getListWithSpec(ctx context.Context, key string, _ storage.ListOptions, listObj runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.getListWithSpec")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	listPtr, err := meta.GetItemsPtr(listObj)
	if err != nil {
		logger.L().Ctx(ctx).Error("getListWithSpec - get items ptr failed", helpers.Error(err), helpers.String("key", key))
		return err
	}
	v, err := conversion.EnforcePtr(listPtr)
	if err != nil || v.Kind() != reflect.Slice {
		logger.L().Ctx(ctx).Error("getListWithSpec - need ptr to slice", helpers.Error(err), helpers.String("key", key))
		return fmt.Errorf("need ptr to slice: %v", err)
	}

	p := filepath.Join(s.root, key)
	var payloadFiles []string

	payloadPath := makePayloadPath(p)
	if exists, _ := afero.Exists(s.appFs, payloadPath); exists {
		// key refers to one object
		payloadFiles = append(payloadFiles, payloadPath)
	} else {
		_ = afero.Walk(s.appFs, p, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && IsPayloadFile(path) {
				payloadFiles = append(payloadFiles, path)
			}
			return nil
		})
	}
	for _, payloadFile := range payloadFiles {
		if err := s.appendGobObjectFromFile(ctx, payloadFile, v); err != nil {
			logger.L().Ctx(ctx).Error("getListWithSpec - appending Gob object from file failed", helpers.Error(err), helpers.String("path", payloadFile))
		}
	}
	return nil
}

func (s *StorageImpl) getStateFromObject(ctx context.Context, obj runtime.Object) (*objState, error) {
	state := &objState{
		obj:  obj,
		meta: &storage.ResponseMeta{},
	}

	rv, err := s.versioner.ObjectResourceVersion(obj)
	if err != nil {
		logger.L().Ctx(ctx).Error("getStateFromObject - get object resource version failed", helpers.Error(err), helpers.Interface("object", obj))
		return nil, fmt.Errorf("couldn't get resource version: %v", err)
	}
	state.rev = int64(rv)
	state.meta.ResourceVersion = uint64(state.rev)

	state.data, err = json.Marshal(obj)
	if err != nil {
		logger.L().Ctx(ctx).Error("getStateFromObject - marshal object failed", helpers.Error(err), helpers.Interface("object", obj))
		return nil, err
	}
	if err := s.versioner.UpdateObject(state.obj, rv); err != nil {
		logger.L().Ctx(ctx).Error("getStateFromObject - update object version failed", helpers.Error(err), helpers.Interface("object", obj))
	}
	return state, nil
}

// GuaranteedUpdate keeps calling 'tryUpdate()' to update key 'key' (of type 'destination')
// retrying the update until success if there is index conflict.
// Note that object passed to tryUpdate may change across invocations of tryUpdate() if
// other writers are simultaneously updating it, so tryUpdate() needs to take into account
// the current contents of the object when deciding how the update object should look.
// If the key doesn't exist, it will return NotFound storage error if ignoreNotFound=false
// else `destination` will be set to the zero value of its type.
// If the eventual successful invocation of `tryUpdate` returns an output with the same serialized
// contents as the input, it won't perform any update, but instead set `destination` to an object with those
// contents.
// If 'cachedExistingObject' is non-nil, it can be used as a suggestion about the
// current version of the object to avoid read operation from storage to get it.
// However, the implementations have to retry in case suggestion is stale.
//
// Example:
//
// s := /* implementation of Interface */
// err := s.GuaranteedUpdate(
//
//	 "myKey", &MyType{}, true, preconditions,
//	 func(input runtime.Object, res ResponseMeta) (runtime.Object, *uint64, error) {
//	   // Before each invocation of the user defined function, "input" is reset to
//	   // current contents for "myKey" in database.
//	   curr := input.(*MyType)  // Guaranteed to succeed.
//
//	   // Make the modification
//	   curr.Counter++
//
//	   // Return the modified object - return an error to stop iterating. Return
//	   // a uint64 to alter the TTL on the object, or nil to keep it the same value.
//	   return cur, nil, nil
//	}, cachedExistingObject
//
// )
func (s *StorageImpl) GuaranteedUpdate(
	ctx context.Context, key string, metaOut runtime.Object, ignoreNotFound bool,
	preconditions *storage.Preconditions, tryUpdate storage.UpdateFunc, cachedExistingObject runtime.Object) error {
	conn, err := s.pool.Take(context.Background())
	if err != nil {
		return fmt.Errorf("take connection: %w", err)
	}
	defer s.pool.Put(conn)
	return s.GuaranteedUpdateWithConn(ctx, conn, key, metaOut, ignoreNotFound, preconditions, tryUpdate, cachedExistingObject, "")
}

func (s *StorageImpl) GuaranteedUpdateWithConn(
	ctx context.Context, conn *sqlite.Conn, key string, metaOut runtime.Object, ignoreNotFound bool,
	preconditions *storage.Preconditions, tryUpdate storage.UpdateFunc, cachedExistingObject runtime.Object, checksum string) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.GuaranteedUpdate")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	_, spanLock := otel.Tracer("").Start(ctx, "waiting for lock")
	beforeLock := time.Now()
	err := s.locks.Lock(ctx, key)
	if err != nil {
		logger.L().Debug("GuaranteedUpdate - lock failed", helpers.Error(err), helpers.String("key", key))
		return apierrors.NewTimeoutError(fmt.Sprintf("lock: %v", err), 0)
	}
	defer s.locks.Unlock(key)
	spanLock.End()
	lockDuration := time.Since(beforeLock)
	if lockDuration > time.Second {
		logger.L().Debug("GuaranteedUpdate/", helpers.String("key", key), helpers.String("lockDuration", lockDuration.String()))
	}

	// key preparation is skipped
	// otel span tracking is skipped

	v, err := conversion.EnforcePtr(metaOut)
	if err != nil {
		logger.L().Ctx(ctx).Error("GuaranteedUpdate - unable to convert output object to pointer", helpers.Error(err), helpers.String("key", key))
		return fmt.Errorf("unable to convert output object to pointer: %v", err)
	}

	getCurrentState := func() (*objState, error) {
		objPtr := reflect.New(v.Type()).Interface().(runtime.Object)
		err := s.get(ctx, conn, key, storage.GetOptions{IgnoreNotFound: ignoreNotFound}, objPtr)
		if err != nil {
			logger.L().Ctx(ctx).Error("GuaranteedUpdate - get failed", helpers.Error(err), helpers.String("key", key))
			return nil, err
		}
		return s.getStateFromObject(ctx, objPtr)
	}

	var origState *objState
	var origStateIsCurrent bool
	if cachedExistingObject != nil {
		origState, err = s.getStateFromObject(ctx, cachedExistingObject)
	} else {
		origState, err = getCurrentState()
		origStateIsCurrent = true
	}
	if err != nil {
		logger.L().Ctx(ctx).Error("GuaranteedUpdate - get original state failed", helpers.Error(err), helpers.String("key", key))
		return err
	}

	// check object size
	annotations := origState.obj.(metav1.Object).GetAnnotations()
	if annotations != nil && annotations[helpersv1.StatusMetadataKey] == helpersv1.TooLarge {
		logger.L().Debug("GuaranteedUpdate - already too large object, skipping update", helpers.String("key", key))
		// no change, return the original object
		v.Set(reflect.ValueOf(origState.obj).Elem())
		return nil
	}

	for {
		// run preconditions
		if err := preconditions.Check(key, origState.obj); err != nil {
			// If our data is already up-to-date, return the error
			if origStateIsCurrent {
				logger.L().Ctx(ctx).Error("GuaranteedUpdate - preconditions check failed", helpers.Error(err), helpers.String("key", key))
				return err
			}

			// It's possible we were working with stale data
			// Actually fetch
			origState, err = getCurrentState()
			if err != nil {
				logger.L().Ctx(ctx).Error("GuaranteedUpdate - get state failed", helpers.Error(err), helpers.String("key", key))
				return err
			}
			origStateIsCurrent = true
			// Retry
			continue
		}

		// run tryUpdate
		ret, _, err := tryUpdate(origState.obj, storage.ResponseMeta{})
		if err != nil {
			// If our data is already up-to-date, return the error
			if origStateIsCurrent {
				if !apierrors.IsNotFound(err) && !apierrors.IsInvalid(err) {
					logger.L().Ctx(ctx).Error("GuaranteedUpdate - tryUpdate func failed", helpers.Error(err), helpers.String("key", key))
				}
				logger.L().Debug("GuaranteedUpdate - tryUpdate func failed", helpers.Error(err), helpers.String("key", key))
				return err
			}

			// It's possible we were working with stale data
			// Remember the revision of the potentially stale data and the resulting update error
			cachedRev := origState.rev
			cachedUpdateErr := err

			// Actually fetch
			origState, err = getCurrentState()
			if err != nil {
				logger.L().Ctx(ctx).Error("GuaranteedUpdate - get state failed", helpers.Error(err), helpers.String("key", key))
				return err
			}
			origStateIsCurrent = true

			// it turns out our cached data was not stale, return the error
			if cachedRev == origState.rev {
				if !apierrors.IsNotFound(err) && !apierrors.IsInvalid(err) {
					logger.L().Ctx(ctx).Error("GuaranteedUpdate - tryUpdate func failed", helpers.Error(err), helpers.String("key", key))
				}
				logger.L().Debug("GuaranteedUpdate - tryUpdate func failed", helpers.Error(err), helpers.String("key", key))
				return cachedUpdateErr
			}

			// Retry
			continue
		}

		// call processor on object to be saved
		if err := s.processor.PreSave(ctx, conn, ret); err != nil {
			if errors.Is(err, ObjectTooLargeError) {
				// revert spec
				ret = origState.obj.DeepCopyObject() // FIXME this is expensive
				// update annotations with the new state
				metadata := ret.(metav1.Object)
				annotations := metadata.GetAnnotations()
				annotations[helpersv1.StatusMetadataKey] = helpersv1.TooLarge
				metadata.SetAnnotations(annotations)
				logger.L().Debug("GuaranteedUpdate - too large object, skipping update", helpers.String("key", key))
				// we don't return here as we still need to save the object with updated annotations
			} else {
				logger.L().Debug("GuaranteedUpdate - processor.PreSave failed", helpers.Error(err), helpers.String("key", key))
				return err
			}
		}

		// check if the object is the same as the original
		orig := origState.obj.DeepCopyObject() // FIXME this is expensive
		_ = s.processor.PreSave(ctx, conn, orig)
		if reflect.DeepEqual(orig, ret) {
			logger.L().Debug("GuaranteedUpdate - tryUpdate returned the same object, no update needed", helpers.String("key", key))
			// no change, return the original object
			v.Set(reflect.ValueOf(origState.obj).Elem())
			return nil
		}

		// save to disk and fill into metaOut
		if err := s.saveObject(conn, key, ret, metaOut, checksum); err != nil {
			logger.L().Ctx(ctx).Error("GuaranteedUpdate - save object failed", helpers.Error(err), helpers.String("key", key))
			return err
		}
		// Only successful updates should produce modification events
		s.watchDispatcher.Modified(key, metaOut, ret)
		return nil
	}
}

// Count returns number of different entries under the key (generally being path prefix).
func (s *StorageImpl) Count(key string) (int64, error) {
	logger.L().Debug("Custom storage count", helpers.String("key", key))
	conn, err := s.pool.Take(context.Background())
	if err != nil {
		return 0, fmt.Errorf("take connection: %w", err)
	}
	defer s.pool.Put(conn)
	return countMetadata(conn, key)
}

// RequestWatchProgress fulfills the storage.Interface
//
// Its function is only relevant to etcd.
func (s *StorageImpl) RequestWatchProgress(context.Context) error {
	return nil
}

// GetByNamespace returns all objects in a given namespace, given their api version and kind.
func (s *StorageImpl) GetByNamespace(ctx context.Context, apiVersion, kind, namespace string, listObj runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.GetByNamespace")
	span.SetAttributes(attribute.String("apiVersion", apiVersion), attribute.String("kind", kind), attribute.String("namespace", namespace))
	defer span.End()

	p := filepath.Join(apiVersion, kind, namespace)

	return s.getListWithSpec(ctx, p, storage.ListOptions{}, listObj)
}

// GetByCluster returns all objects in a given cluster, given their api version and kind.
func (s *StorageImpl) GetByCluster(ctx context.Context, apiVersion, kind string, listObj runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.GetClusterScopedResource")
	defer span.End()

	p := filepath.Join(apiVersion, kind)

	return s.getListWithSpec(ctx, p, storage.ListOptions{}, listObj)
}

// appendGobObjectFromFile unmarshalls a Gob file into a runtime.Object and appends it to the underlying list object.
func (s *StorageImpl) appendGobObjectFromFile(ctx context.Context, path string, v reflect.Value) error {
	key := s.keyFromPath(path)
	err := s.locks.RLock(ctx, key)
	if err != nil {
		return apierrors.NewTimeoutError(fmt.Sprintf("rlock: %v", err), 0)
	}
	defer s.locks.RUnlock(key)
	payloadFile, err := s.appFs.OpenFile(path, syscall.O_DIRECT|os.O_RDONLY, 0)
	if err != nil {
		// skip if file is not readable, maybe it was deleted
		return nil
	}
	defer func() {
		_ = payloadFile.Close()
	}()

	elem := v.Type().Elem()
	obj := reflect.New(elem).Interface().(runtime.Object)

	decoder := gob.NewDecoder(NewDirectIOReader(payloadFile))
	if err := decoder.Decode(obj); err != nil {
		return err
	}

	v.Set(reflect.Append(v, reflect.ValueOf(obj).Elem()))

	return nil
}

func (s *StorageImpl) CalculateChecksum(in runtime.Object) (string, error) {
	// convert to v1beta1 object
	obj, err := s.scheme.ConvertToVersion(in, v1beta1.SchemeGroupVersion)
	if err != nil {
		return "", fmt.Errorf("convert to v1beta1: %w", err)
	}
	utils.RemoveManagedFields(obj.(metav1.Object))
	// add type meta information to the object
	sl := strings.Split(reflect.ValueOf(obj).Elem().Type().String(), ".")
	for len(sl) < 2 {
		sl = append(sl, "")
	}
	obj.GetObjectKind().SetGroupVersionKind(schema.GroupVersionKind{
		Group:   softwarecomposition.GroupName,
		Version: sl[0],
		Kind:    sl[1],
	})
	b, err := json.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("marshal object: %w", err)
	}
	hash, err := utils.CanonicalHash(b)
	if err != nil {
		return "", fmt.Errorf("calculate checksum: %w", err)
	}
	return hash, nil
}

func getNamespaceFromKey(key string) string {
	keySplit := strings.Split(key, "/")
	if len(keySplit) != 4 {
		return ""
	}

	return keySplit[3]
}

// replaceKeyForKind encapsulates the logic of replacing the kind in the key with the given kind.
func replaceKeyForKind(key string, kind string) string {
	keySplit := strings.Split(key, "/")
	keySplit[2] = strings.ToLower(kind)

	return strings.Join(keySplit, "/")
}

type immutableStorage struct{}

// Create is not supported for immutable objects. Objects are generated on the fly and not stored.
func (immutableStorage) Create(_ context.Context, key string, _, _ runtime.Object, _ uint64) error {
	return storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

// Delete is not supported for immutable objects. Objects are generated on the fly and not stored.
func (immutableStorage) Delete(_ context.Context, key string, _ runtime.Object, _ *storage.Preconditions, _ storage.ValidateObjectFunc, _ runtime.Object, _ storage.DeleteOptions) error {
	return storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

// Watch is not supported for immutable objects. Objects are generated on the fly and not stored.
func (immutableStorage) Watch(_ context.Context, _ string, _ storage.ListOptions) (watch.Interface, error) {
	return watch.NewEmptyWatch(), nil
}

// GuaranteedUpdate is not supported for immutable objects. Objects are generated on the fly and not stored.
func (immutableStorage) GuaranteedUpdate(_ context.Context, key string, _ runtime.Object, _ bool, _ *storage.Preconditions, _ storage.UpdateFunc, _ runtime.Object) error {
	return storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

// Count is not supported for immutable objects. Objects are generated on the fly and not stored.
func (immutableStorage) Count(key string) (int64, error) {
	return 0, storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

func (immutableStorage) ReadinessCheck() error {
	return nil
}

// RequestWatchProgress fulfills the storage.Interface
//
// It’s function is only relevant to etcd.
func (immutableStorage) RequestWatchProgress(context.Context) error { return nil }

// Versioner Returns fixed versioner associated with this interface.
func (immutableStorage) Versioner() storage.Versioner {
	return storage.APIObjectVersioner{}
}
