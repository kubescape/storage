package file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"

	"github.com/spf13/afero"
	"github.com/spyzhov/ajson"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"

	"k8s.io/klog/v2"
)

const (
	defaultChanSize = 100
)

type EventBus struct {
	eventCh chan watch.Event
}

func NewEventBus(wc chan watch.Event) *EventBus {
	return &EventBus{wc}
}

func (w *EventBus) Stop() {
}

func (w *EventBus) ResultChan() <-chan watch.Event {
	return w.eventCh
}

type event struct {
	key string
}

type objState struct {
	obj   runtime.Object
	meta  *storage.ResponseMeta
	rev   int64
	data  []byte
	stale bool
}

// Interface offers a common interface for object marshaling/unmarshaling operations and
// hides all the storage-related operations behind it.
type StorageImpl struct {
	appFs     afero.Fs
	eventBus  *EventBus
	lock      sync.RWMutex
	root      string
	versioner storage.Versioner
}

var _ storage.Interface = &StorageImpl{}

func NewStorageImpl(appFs afero.Fs, root string) storage.Interface {
	watchChan := make(chan watch.Event, defaultChanSize)

	eventBus := NewEventBus(watchChan)
	return &StorageImpl{
		appFs:     appFs,
		eventBus:  eventBus,
		root:      root,
		versioner: storage.APIObjectVersioner{},
	}
}

func (s *StorageImpl) getPath(key string) string {
	return filepath.Join(s.root, key) + ".json"
}

// Returns Versioner associated with this interface.
func (s *StorageImpl) Versioner() storage.Versioner {
	return s.versioner
}

// Create adds a new object at a key unless it already exists. 'ttl' is time-to-live
// in seconds (0 means forever). If no error is returned and out is not nil, out will be
// set to the read value from database.
func (s *StorageImpl) Create(_ context.Context, key string, obj, out runtime.Object, _ uint64) error {
	klog.Warningf("Custom storage create: %s", key)
	p := s.getPath(key)
	s.lock.Lock()
	defer s.lock.Unlock()
	err := s.appFs.MkdirAll(filepath.Dir(p), 0755)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	err = afero.WriteFile(s.appFs, p, b, 0644)
	if err != nil {
		return err
	}
	if out != nil {
		err = json.Unmarshal(b, out)
		if err != nil {
			return err
		}
	}
	event := watch.Event{Type: watch.Added, Object: obj}
	s.eventBus.eventCh <- event
	klog.Warningf("Custom storage published event: %v", event)
	return nil
}

// Delete removes the specified key and returns the value that existed at that spot.
// If key didn't exist, it will return NotFound storage error.
// If 'cachedExistingObject' is non-nil, it can be used as a suggestion about the
// current version of the object to avoid read operation from storage to get it.
// However, the implementations have to retry in case suggestion is stale.
func (s *StorageImpl) Delete(_ context.Context, key string, out runtime.Object, _ *storage.Preconditions, _ storage.ValidateObjectFunc, _ runtime.Object) error {
	klog.Warningf("Custom storage delete: %s", key)
	p := s.getPath(key)
	s.lock.Lock()
	defer s.lock.Unlock()
	if exists, _ := afero.Exists(s.appFs, p); !exists {
		return storage.NewKeyNotFoundError(key, 0)
	}
	b, err := afero.ReadFile(s.appFs, p)
	if err != nil {
		return err
	}
	err = json.Unmarshal(b, out)
	if err != nil {
		return err
	}
	return s.appFs.Remove(p)
}

// Watch begins watching the specified key. Events are decoded into API objects,
// and any items selected by 'p' are sent down to returned watch.Interface.
// resourceVersion may be used to specify what version to begin watching,
// which should be the current resourceVersion, and no longer rv+1
// (e.g. reconnecting without missing any updates).
// If resource version is "0", this interface will get current object at given key
// and send it in an "ADDED" event, before watch starts.
func (s *StorageImpl) Watch(ctx context.Context, key string, opts storage.ListOptions) (watch.Interface, error) {
	klog.Warningf("Custom storage watch: %s", key)
	return s.eventBus, nil
}

// Get unmarshals object found at key into objPtr. On a not found error, will either
// return a zero object of the requested type, or an error, depending on 'opts.ignoreNotFound'.
// Treats empty responses and nil response nodes exactly like a not found error.
// The returned contents may be delayed, but it is guaranteed that they will
// match 'opts.ResourceVersion' according 'opts.ResourceVersionMatch'.
func (s *StorageImpl) Get(_ context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	klog.Warningf("Custom storage get: %s", key)
	p := s.getPath(key)
	s.lock.RLock()
	defer s.lock.RUnlock()
	if exists, _ := afero.Exists(s.appFs, p); exists {
		b, err := afero.ReadFile(s.appFs, p)
		if err != nil {
			return err
		}
		err = json.Unmarshal(b, objPtr)
		if err != nil {
			return err
		}
	} else if opts.IgnoreNotFound {
		return runtime.SetZeroValue(objPtr)
	} else {
		return storage.NewKeyNotFoundError(key, 0)
	}
	return nil
}

// GetList unmarshalls objects found at key into a *List api object (an object
// that satisfies runtime.IsList definition).
// If 'opts.Recursive' is false, 'key' is used as an exact match. If `opts.Recursive'
// is true, 'key' is used as a prefix.
// The returned contents may be delayed, but it is guaranteed that they will
// match 'opts.ResourceVersion' according 'opts.ResourceVersionMatch'.
func (s *StorageImpl) GetList(_ context.Context, key string, _ storage.ListOptions, listObj runtime.Object) error {
	klog.Warningf("Custom storage getlist: %s", key)
	listPtr, err := meta.GetItemsPtr(listObj)
	if err != nil {
		return err
	}
	v, err := conversion.EnforcePtr(listPtr)
	if err != nil || v.Kind() != reflect.Slice {
		return fmt.Errorf("need ptr to slice: %v", err)
	}
	p := filepath.Join(s.root, key)
	s.lock.RLock()
	defer s.lock.RUnlock()
	return afero.Walk(s.appFs, p, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			// read from file
			b, err := afero.ReadFile(s.appFs, path)
			if err != nil {
				return nil
			}
			// remove spec to save bandwidth
			root, err := ajson.Unmarshal(b)
			if err != nil {
				return nil
			}
			_ = root.DeleteKey("Spec") // ignore error
			b, err = ajson.Marshal(root)
			if err != nil {
				return nil
			}
			// unmarshal into object
			elem := v.Type().Elem()
			obj := reflect.New(elem).Interface().(runtime.Object)
			err = json.Unmarshal(b, obj)
			if err != nil {
				return nil
			}
			// append to list
			v.Set(reflect.Append(v, reflect.ValueOf(obj).Elem()))
		}
		return nil
	})
}

func (s *StorageImpl) getStateFromObject(obj runtime.Object) (*objState, error) {
	state := &objState{
		obj:  obj,
		meta: &storage.ResponseMeta{},
	}

	rv, err := s.versioner.ObjectResourceVersion(obj)
	if err != nil {
		return nil, fmt.Errorf("couldn't get resource version: %v", err)
	}
	state.rev = int64(rv)
	state.meta.ResourceVersion = uint64(state.rev)

	state.data, err = json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	if err := s.versioner.UpdateObject(state.obj, uint64(rv)); err != nil {
		klog.Errorf("failed to update object version: %v", err)
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
	ctx context.Context, key string, destination runtime.Object, ignoreNotFound bool,
	preconditions *storage.Preconditions, tryUpdate storage.UpdateFunc, cachedExistingObject runtime.Object) error {
	klog.Warningf("Custom storage guaranteedupdate: %s", key)

	// key preparation is skipped
	// otel span tracking is skipped

	v, err := conversion.EnforcePtr(destination)
	if err != nil {
		return fmt.Errorf("unable to convert output object to pointer: %v", err)
	}

	getCurrentState := func() (*objState, error) {
		objPtr := reflect.New(v.Type()).Interface().(runtime.Object)
		err := s.Get(ctx, key, storage.GetOptions{IgnoreNotFound: ignoreNotFound}, objPtr)
		if err != nil {
			return nil, err
		}
		return s.getStateFromObject(objPtr)
	}

	var origState *objState
	var origStateIsCurrent bool
	if cachedExistingObject != nil {
		origState, err = s.getStateFromObject(cachedExistingObject)
	} else {
		origState, err = getCurrentState()
		origStateIsCurrent = true
	}
	if err != nil {
		return err
	}

	for {
		// run preconditions
		if err := preconditions.Check(key, origState.obj); err != nil {
			// If our data is already up-to-date, return the error
			if origStateIsCurrent {
				return err
			}

			// It's possible we were working with stale data
			// Actually fetch
			origState, err = getCurrentState()
			if err != nil {
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
				return err
			}

			// It's possible we were working with stale data
			// Remember the revision of the potentially stale data and the resulting update error
			cachedRev := origState.rev
			cachedUpdateErr := err

			// Actually fetch
			origState, err = getCurrentState()
			if err != nil {
				return err
			}
			origStateIsCurrent = true

			// it turns out our cached data was not stale, return the error
			if cachedRev == origState.rev {
				return cachedUpdateErr
			}

			// Retry
			continue
		}

		// fill into destination
		reflect.ValueOf(destination).Elem().Set(reflect.ValueOf(ret).Elem())

		// done
		return nil
	}
}

// Count returns number of different entries under the key (generally being path prefix).
func (s *StorageImpl) Count(key string) (int64, error) {
	klog.Warningf("Custom storage count: %s", key)
	p := filepath.Join(s.root, key)
	s.lock.RLock()
	defer s.lock.RUnlock()
	n := 0
	err := afero.Walk(s.appFs, p, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			n++
		}
		return nil
	})
	return int64(n), err
}
