package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/spf13/afero"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
)

const (
	JsonExt                  = ".j"
	MetadataExt              = ".m"
	DefaultStorageRoot       = "/data"
	StorageV1Beta1ApiVersion = "spdx.softwarecomposition.kubescape.io/v1beta1"
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
	watchDispatcher watchDispatcher
	lock            *sync.RWMutex
	root            string
	versioner       storage.Versioner
}

// StorageQuerier wraps the storage.Interface and adds some extra methods which are used by the storage implementation.
type StorageQuerier interface {
	storage.Interface
	GetByNamespace(ctx context.Context, apiVersion, kind, namespace string, listObj runtime.Object) error
	GetByCluster(ctx context.Context, apiVersion, kind string, listObj runtime.Object) error
	GetClusterScopedResource(ctx context.Context, apiVersion, kind string, listObj runtime.Object) error
}

var _ storage.Interface = &StorageImpl{}

var _ StorageQuerier = &StorageImpl{}

func NewStorageImpl(appFs afero.Fs, root string) StorageQuerier {
	return &StorageImpl{
		appFs:           appFs,
		watchDispatcher: newWatchDispatcher(),
		lock:            &sync.RWMutex{},
		root:            root,
		versioner:       storage.APIObjectVersioner{},
	}
}

// Versioner Returns Versioner associated with this interface.
func (s *StorageImpl) Versioner() storage.Versioner {
	return s.versioner
}

func removeSpec(obj runtime.Object) {
	val := reflect.ValueOf(obj).Elem()
	spec := val.FieldByName("Spec")
	if spec.IsValid() {
		spec.Set(reflect.Zero(spec.Type()))
	}
}

// makePayloadPath returns a path for the payload file
func makePayloadPath(path string) string {
	return path + JsonExt
}

// makeMetadataPath returns a path for the metadata file
func makeMetadataPath(path string) string {
	return path + MetadataExt
}

// isMetadataFile returns true if a given file at `path` is an object metadata file, else false
func IsMetadataFile(path string) bool {
	return strings.HasSuffix(path, MetadataExt)
}

// isPayloadFile returns true if a given file at `path` is an object payload file, else false
func isPayloadFile(path string) bool {
	return !IsMetadataFile(path)
}

func (s *StorageImpl) writeFiles(ctx context.Context, key string, obj runtime.Object, metaOut runtime.Object) error {
	// set resourceversion
	if version, _ := s.versioner.ObjectResourceVersion(obj); version == 0 {
		if err := s.versioner.UpdateObject(obj, 1); err != nil {
			return fmt.Errorf("set resourceVersion: %w", err)
		}
	}
	// prepare path
	p := filepath.Join(s.root, key)
	_, spanLock := otel.Tracer("").Start(ctx, "waiting for lock")
	s.lock.Lock()
	spanLock.End()
	defer s.lock.Unlock()
	if err := s.appFs.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	// prepare payload file
	payloadFile, err := s.appFs.OpenFile(makePayloadPath(p), os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open payload file: %w", err)
	}
	// prepare metadata file
	metadataFile, err := s.appFs.OpenFile(makeMetadataPath(p), os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open metadata file: %w", err)
	}
	// prepare payload encoder
	payloadEncoder := json.NewEncoder(payloadFile)
	// prepare metadata encoder
	metadataEncoder := json.NewEncoder(metadataFile)
	// write payload
	if err := payloadEncoder.Encode(obj); err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}
	// remove spec from payload
	removeSpec(obj)
	// write metadata
	if err := metadataEncoder.Encode(obj); err != nil {
		return fmt.Errorf("encode metadata: %w", err)
	}
	// eventually fill metaOut
	if metaOut != nil {
		val := reflect.ValueOf(metaOut)
		if val.Kind() == reflect.Ptr {
			// Dereference the pointer
			val = val.Elem()
		}
		// copy obj into metaOut
		val.Set(reflect.ValueOf(obj).Elem())
	}
	return nil
}

// Create adds a new object at a key even when it already exists. 'ttl' is time-to-live
// in seconds (and is ignored). If no error is returned and out is not nil, out will be
// set to the read value from database.
func (s *StorageImpl) Create(ctx context.Context, key string, obj, metaOut runtime.Object, _ uint64) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.Create")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	// resourceversion should not be set on create
	if version, err := s.versioner.ObjectResourceVersion(obj); err == nil && version != 0 {
		msg := "resourceVersion should not be set on objects to be created"
		logger.L().Ctx(ctx).Error(msg)
		return errors.New(msg)
	}
	// write files
	if err := s.writeFiles(ctx, key, obj, metaOut); err != nil {
		logger.L().Ctx(ctx).Error("write files failed", helpers.Error(err), helpers.String("key", key))
		return err
	}

	// publish event to watchers
	s.watchDispatcher.Added(key, metaOut)
	return nil
}

// Delete removes the specified key and returns the value that existed at that spot.
// If key didn't exist, it will return NotFound storage error.
// If 'cachedExistingObject' is non-nil, it can be used as a suggestion about the
// current version of the object to avoid read operation from storage to get it.
// However, the implementations have to retry in case suggestion is stale.
func (s *StorageImpl) Delete(ctx context.Context, key string, metaOut runtime.Object, _ *storage.Preconditions, _ storage.ValidateObjectFunc, _ runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.Delete")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	p := filepath.Join(s.root, key)
	_, spanLock := otel.Tracer("").Start(ctx, "waiting for lock")
	s.lock.Lock()
	spanLock.End()
	defer s.lock.Unlock()
	// read metadata file
	file, err := s.appFs.Open(makeMetadataPath(p))
	if err != nil {
		if errors.Is(err, afero.ErrFileNotFound) {
			return storage.NewKeyNotFoundError(key, 0)
		}
		logger.L().Ctx(ctx).Error("read file failed", helpers.Error(err), helpers.String("key", key))
		return err
	}
	// delete payload and metadata files
	err = s.appFs.Remove(makePayloadPath(p))
	if err != nil {
		logger.L().Ctx(ctx).Error("remove json file failed", helpers.Error(err), helpers.String("key", key))
	}
	err = s.appFs.Remove(makeMetadataPath(p))
	if err != nil {
		logger.L().Ctx(ctx).Error("remove metadata file failed", helpers.Error(err), helpers.String("key", key))
	}
	// try to fill metaOut
	decoder := json.NewDecoder(file)
	err = decoder.Decode(metaOut)
	if err != nil {
		logger.L().Ctx(ctx).Error("json unmarshal failed", helpers.Error(err), helpers.String("key", key))
		return err
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
func (s *StorageImpl) Watch(ctx context.Context, key string, _ storage.ListOptions) (watch.Interface, error) {
	_, span := otel.Tracer("").Start(ctx, "StorageImpl.Watch")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	newWatcher := newWatcher(make(chan watch.Event))
	s.watchDispatcher.Register(key, newWatcher)
	return newWatcher, nil
}

// Get unmarshals object found at key into objPtr. On a not found error, will either
// return a zero object of the requested type, or an error, depending on 'opts.ignoreNotFound'.
// Treats empty responses and nil response nodes exactly like a not found error.
// The returned contents may be delayed, but it is guaranteed that they will
// match 'opts.ResourceVersion' according 'opts.ResourceVersionMatch'.
func (s *StorageImpl) Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.Get")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	p := filepath.Join(s.root, key)
	_, spanLock := otel.Tracer("").Start(ctx, "waiting for lock")
	s.lock.RLock()
	spanLock.End()
	defer s.lock.RUnlock()
	file, err := s.appFs.Open(makePayloadPath(p))
	if err != nil {
		if errors.Is(err, afero.ErrFileNotFound) {
			if opts.IgnoreNotFound {
				return runtime.SetZeroValue(objPtr)
			} else {
				return storage.NewKeyNotFoundError(key, 0)
			}
		}
		logger.L().Ctx(ctx).Error("read file failed", helpers.Error(err), helpers.String("key", key))
		return err
	}
	decoder := json.NewDecoder(file)
	err = decoder.Decode(objPtr)
	if err != nil {
		logger.L().Ctx(ctx).Error("json unmarshal failed", helpers.Error(err), helpers.String("key", key))
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
func (s *StorageImpl) GetList(ctx context.Context, key string, _ storage.ListOptions, listObj runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.GetList")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	listPtr, err := meta.GetItemsPtr(listObj)
	if err != nil {
		logger.L().Ctx(ctx).Error("get items ptr failed", helpers.Error(err), helpers.String("key", key))
		return err
	}
	v, err := conversion.EnforcePtr(listPtr)
	if err != nil || v.Kind() != reflect.Slice {
		logger.L().Ctx(ctx).Error("need ptr to slice", helpers.Error(err), helpers.String("key", key))
		return fmt.Errorf("need ptr to slice: %v", err)
	}

	p := filepath.Join(s.root, key)
	var files []string
	_, spanLock := otel.Tracer("").Start(ctx, "waiting for lock")
	s.lock.RLock()
	spanLock.End()

	metadataPath := makeMetadataPath(p)
	if exists, _ := afero.Exists(s.appFs, metadataPath); exists {
		files = append(files, metadataPath)
	} else {
		_ = afero.Walk(s.appFs, p, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() && IsMetadataFile(path) {
				files = append(files, path)
			}
			return nil
		})
	}
	s.lock.RUnlock()
	for _, path := range files {
		// we need to read the whole file
		file, err := s.appFs.Open(path)
		if err != nil {
			// skip if file is not readable, maybe it was deleted
			continue
		}

		obj, err := getUnmarshaledRuntimeObject(v, file)
		if err != nil {
			logger.L().Ctx(ctx).Error("unmarshal file failed", helpers.Error(err), helpers.String("path", path))
			continue
		}
		// append to list
		v.Set(reflect.Append(v, reflect.ValueOf(obj).Elem()))
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
		logger.L().Ctx(ctx).Error("get object resource version failed", helpers.Error(err), helpers.Interface("object", obj))
		return nil, fmt.Errorf("couldn't get resource version: %v", err)
	}
	state.rev = int64(rv)
	state.meta.ResourceVersion = uint64(state.rev)

	state.data, err = json.Marshal(obj)
	if err != nil {
		logger.L().Ctx(ctx).Error("marshal object failed", helpers.Error(err), helpers.Interface("object", obj))
		return nil, err
	}
	if err := s.versioner.UpdateObject(state.obj, rv); err != nil {
		logger.L().Ctx(ctx).Error("update object version failed", helpers.Error(err), helpers.Interface("object", obj))
	}
	return state, nil
}

// GuaranteedUpdate keeps calling 'tryUpdate()' to update key 'key' (of type 'destination')
// retrying the update until success if there is index conflict.
// Note that object passed to tryUpdate may change across invocations of tryUpdate() if
// other writers are simultaneously updating it, so tryUpdate() needs to take into account
// the current contents of the object when deciding how the update object should look.
// If the key doesn't exist, it will return NotFound storage error if ignoreNotFound=false
// else `destination` will be set to the zero value of it's type.
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
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.GuaranteedUpdate")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()

	// key preparation is skipped
	// otel span tracking is skipped

	v, err := conversion.EnforcePtr(metaOut)
	if err != nil {
		logger.L().Ctx(ctx).Error("unable to convert output object to pointer", helpers.Error(err), helpers.String("key", key))
		return fmt.Errorf("unable to convert output object to pointer: %v", err)
	}

	getCurrentState := func() (*objState, error) {
		objPtr := reflect.New(v.Type()).Interface().(runtime.Object)
		err := s.Get(ctx, key, storage.GetOptions{IgnoreNotFound: ignoreNotFound}, objPtr)
		if err != nil {
			logger.L().Ctx(ctx).Error("get failed", helpers.Error(err), helpers.String("key", key))
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
		logger.L().Ctx(ctx).Error("get original state failed", helpers.Error(err), helpers.String("key", key))
		return err
	}

	for {
		// run preconditions
		if err := preconditions.Check(key, origState.obj); err != nil {
			// If our data is already up-to-date, return the error
			if origStateIsCurrent {
				logger.L().Ctx(ctx).Error("preconditions check failed", helpers.Error(err), helpers.String("key", key))
				return err
			}

			// It's possible we were working with stale data
			// Actually fetch
			origState, err = getCurrentState()
			if err != nil {
				logger.L().Ctx(ctx).Error("get state failed", helpers.Error(err), helpers.String("key", key))
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
				logger.L().Ctx(ctx).Error("tryUpdate func failed", helpers.Error(err), helpers.String("key", key))
				return err
			}

			// It's possible we were working with stale data
			// Remember the revision of the potentially stale data and the resulting update error
			cachedRev := origState.rev
			cachedUpdateErr := err

			// Actually fetch
			origState, err = getCurrentState()
			if err != nil {
				logger.L().Ctx(ctx).Error("get state failed", helpers.Error(err), helpers.String("key", key))
				return err
			}
			origStateIsCurrent = true

			// it turns out our cached data was not stale, return the error
			if cachedRev == origState.rev {
				logger.L().Ctx(ctx).Error("tryUpdate func failed", helpers.Error(err), helpers.String("key", key))
				return cachedUpdateErr
			}

			// Retry
			continue
		}

		// save to disk and fill into metaOut
		dup := ret.DeepCopyObject()
		err = s.writeFiles(ctx, key, dup, metaOut)
		if err == nil {
			// Only successful updates should produce modification events
			s.watchDispatcher.Modified(key, metaOut)
		} else {
			logger.L().Ctx(ctx).Error("write files failed", helpers.Error(err), helpers.String("key", key))
		}
		return err
	}
}

// Count returns number of different entries under the key (generally being path prefix).
func (s *StorageImpl) Count(key string) (int64, error) {
	logger.L().Debug("Custom storage count", helpers.String("key", key))
	p := filepath.Join(s.root, key)
	metadataPath := makeMetadataPath(p)

	s.lock.RLock()
	defer s.lock.RUnlock()

	pathExists, _ := afero.Exists(s.appFs, metadataPath)
	pathIsDir, _ := afero.IsDir(s.appFs, metadataPath)
	if pathExists && !pathIsDir {
		return 1, nil
	}

	n := 0
	err := afero.Walk(s.appFs, p, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Only payload files should count towards found objects
		if !info.IsDir() && isPayloadFile(path) {
			n++
		}
		return nil
	})
	return int64(n), err
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

	listPtr, err := meta.GetItemsPtr(listObj)
	if err != nil {
		logger.L().Ctx(ctx).Error("get items ptr failed", helpers.Error(err), helpers.String("apiVersion", apiVersion), helpers.String("kind", kind), helpers.String("namespace", namespace))
		return err
	}

	v, err := conversion.EnforcePtr(listPtr)
	if err != nil || v.Kind() != reflect.Slice {
		logger.L().Ctx(ctx).Error("need ptr to slice", helpers.Error(err), helpers.String("apiVersion", apiVersion), helpers.String("kind", kind), helpers.String("namespace", namespace))
		return err
	}

	p := filepath.Join(s.root, apiVersion, kind, namespace)

	_, spanLock := otel.Tracer("").Start(ctx, "waiting for lock")
	s.lock.RLock()
	defer s.lock.RUnlock()
	spanLock.End()

	// read all json files under the namespace and append to list
	_ = afero.Walk(s.appFs, p, func(path string, info os.FileInfo, err error) error {
		if !isPayloadFile(path) {
			return nil
		}

		if err := s.appendJSONObjectFromFile(path, v); err != nil {
			logger.L().Ctx(ctx).Error("unmarshal file failed", helpers.Error(err), helpers.String("path", path))
		}

		return nil
	})

	return nil
}

// GetClusterScopedResource returns all objects in a given cluster, given their api version and kind.
func (s *StorageImpl) GetClusterScopedResource(ctx context.Context, apiVersion, kind string, listObj runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.GetClusterScopedResource")
	defer span.End()

	listPtr, err := meta.GetItemsPtr(listObj)
	if err != nil {
		logger.L().Ctx(ctx).Error("need ptr to slice", helpers.Error(err), helpers.String("apiVersion", apiVersion), helpers.String("kind", kind))
		return err
	}

	v, err := conversion.EnforcePtr(listPtr)
	if err != nil || v.Kind() != reflect.Slice {
		logger.L().Ctx(ctx).Error("need ptr to slice", helpers.Error(err), helpers.String("apiVersion", apiVersion), helpers.String("kind", kind))
		return err
	}

	p := filepath.Join(s.root, apiVersion, kind)

	_, spanLock := otel.Tracer("").Start(ctx, "waiting for lock")
	s.lock.RLock()
	defer s.lock.RUnlock()
	spanLock.End()

	_ = afero.Walk(s.appFs, p, func(path string, info os.FileInfo, err error) error {
		// the first path is the root path
		if path == p {
			return nil
		}

		_ = afero.Walk(s.appFs, path, func(subPath string, info os.FileInfo, err error) error {
			if !isPayloadFile(subPath) {
				return nil
			}

			if err := s.appendJSONObjectFromFile(subPath, v); err != nil {
				logger.L().Ctx(ctx).Error("appending JSON object from file failed", helpers.Error(err), helpers.String("path", path))
			}

			return nil
		})
		return nil
	})

	return nil
}

// GetByCluster returns all objects in a given cluster, given their api version and kind.
func (s *StorageImpl) GetByCluster(ctx context.Context, apiVersion, kind string, listObj runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.GetByCluster")
	defer span.End()

	listPtr, err := meta.GetItemsPtr(listObj)
	if err != nil {
		logger.L().Ctx(ctx).Error("need ptr to slice", helpers.Error(err), helpers.String("apiVersion", apiVersion), helpers.String("kind", kind))
		return err
	}

	v, err := conversion.EnforcePtr(listPtr)
	if err != nil || v.Kind() != reflect.Slice {
		logger.L().Ctx(ctx).Error("need ptr to slice", helpers.Error(err), helpers.String("apiVersion", apiVersion), helpers.String("kind", kind))
		return err
	}

	p := filepath.Join(s.root, apiVersion, kind)

	_, spanLock := otel.Tracer("").Start(ctx, "waiting for lock")
	s.lock.RLock()
	defer s.lock.RUnlock()
	spanLock.End()

	// for each namespace, read all json files and append it to list obj
	_ = afero.Walk(s.appFs, p, func(path string, info os.FileInfo, err error) error {
		// the first path is the root path
		if path == p {
			return nil
		}

		// under the root path, each directory is a namespace
		if info.IsDir() {
			_ = afero.Walk(s.appFs, path, func(subPath string, info os.FileInfo, err error) error {
				if !isPayloadFile(subPath) {
					return nil
				}

				if err := s.appendJSONObjectFromFile(subPath, v); err != nil {
					logger.L().Ctx(ctx).Error("appending JSON object from file failed", helpers.Error(err), helpers.String("path", path))
				}

				return nil
			})
		}
		return nil
	})

	return nil
}

// appendJSONObjectFromFile unmarshalls a json file into a runtime.Object and appends it to the underlying list object.
func (s *StorageImpl) appendJSONObjectFromFile(path string, v reflect.Value) error {
	file, err := s.appFs.Open(path)
	if err != nil {
		// skip if file is not readable, maybe it was deleted
		return nil
	}

	obj, err := getUnmarshaledRuntimeObject(v, file)
	if err != nil {
		return nil
	}

	v.Set(reflect.Append(v, reflect.ValueOf(obj).Elem()))

	return nil
}

func getUnmarshaledRuntimeObject(v reflect.Value, file afero.File) (runtime.Object, error) {
	elem := v.Type().Elem()
	obj := reflect.New(elem).Interface().(runtime.Object)

	decoder := json.NewDecoder(file)
	if err := decoder.Decode(obj); err != nil {
		return nil, err
	}

	return obj, nil
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
