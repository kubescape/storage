package file

import (
	"errors"
	"path"
	"slices"
	"sync"

	"github.com/puzpuzpuz/xsync/v2"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
)

var (
	errInvalidKey = errors.New("Provided key is invalid")
)

// watcher receives and forwards events to its listeners
type watcher struct {
	stopped bool
	eventCh chan watch.Event
	m       sync.RWMutex
}

// newWatcher creates a new watcher with a given channel
func newWatcher(wc chan watch.Event) *watcher {
	return &watcher{
		stopped: false,
		eventCh: wc,
	}
}

func (w *watcher) Stop() {
	w.m.Lock()
	defer w.m.Unlock()
	w.stopped = true
	close(w.eventCh)
}

func (w *watcher) ResultChan() <-chan watch.Event {
	return w.eventCh
}

func (w *watcher) notify(e watch.Event) bool {
	w.m.RLock()
	defer w.m.RUnlock()
	if w.stopped {
		return false
	}

	w.eventCh <- e
	return true
}

type watchersList []*watcher

// watchDispatcher dispatches events to registered watches
//
// TODO(vladklokun): Please keep in mind that this dispatcher does not offer any collection of
// resources left over by the stopped watches! There are multiple ways to go about it:
// 1. On-Stop cleanup, where a watcher would notifies the dispatcher about being stopped, and that it can be cleaned up
// 2. Garbage collection. Periodic background cleanup. Poses challenges as this
// might be a contended concurrent resource.
type watchDispatcher struct {
	watchesByKey *xsync.MapOf[string, watchersList]
}

func newWatchDispatcher() watchDispatcher {
	return watchDispatcher{xsync.NewMapOf[watchersList]()}
}

func extractKeysToNotify(key string) []string {
	if key[0] != '/' {
		return []string{}
	}

	ret := []string{"/"}
	for left := key; left != "/"; left = path.Dir(left) {
		ret = append(ret, left)
	}
	slices.Sort(ret)
	return ret
}

// Register registers a watcher for a given key
func (wd *watchDispatcher) Register(key string, w *watcher) {
	wd.watchesByKey.Compute(key, func(l watchersList, _ bool) (watchersList, bool) {
		return append(l, w), false
	})
}

// Added dispatches an "Added" event to appropriate watchers
func (wd *watchDispatcher) Added(key string, obj runtime.Object) {
	wd.notify(key, watch.Added, obj)
}

// Deleted dispatches a "Deleted" event to appropriate watchers
func (wd *watchDispatcher) Deleted(key string, obj runtime.Object) {
	wd.notify(key, watch.Deleted, obj)
}

// Modified dispatches a "Modified" event to appropriate watchers
func (wd *watchDispatcher) Modified(key string, obj runtime.Object) {
	wd.notify(key, watch.Modified, obj)
}

// notify notifies the listeners of a given key about an event of a given eventType about a given obj
func (wd *watchDispatcher) notify(key string, eventType watch.EventType, obj runtime.Object) {
	// Don’t block callers by publishing in a separate goroutine
	// TODO(ttimonen) This is kind of expensive way to manage queue, yet watchers might block each other.
	go func() {
		event := watch.Event{Type: eventType, Object: obj}
		for _, part := range extractKeysToNotify(key) {
			ws, _ := wd.watchesByKey.Load(part)
			for _, w := range ws {
				w.notify(event)
			}
		}
	}()
}
