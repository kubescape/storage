package file

import (
	"errors"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"github.com/puzpuzpuz/xsync/v2"
)

var (
	errInvalidKey = errors.New("Provided key is invalid")
)

// watcher receives and forwards events to its listeners
type watcher struct {
	stopped bool
	eventCh chan watch.Event
}

// newWatcher creates a new watcher with a given channel
func newWatcher(wc chan watch.Event) *watcher {
	return &watcher{false, wc}
}

func (w *watcher) Stop() {
	w.stopped = true
	close(w.eventCh)
}

func (w *watcher) ResultChan() <-chan watch.Event {
	return w.eventCh
}

func (w *watcher) notify(e watch.Event) bool {
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
	wbk := xsync.NewMapOf[watchersList]()
	return watchDispatcher{watchesByKey: wbk}
}

func exractKeysToNotify(key string) ([]string, error) {
	resKeys := []string{}
	if key[0] != '/' {
		return resKeys, errInvalidKey
	}

	sep := '/'
	currentKey := strings.Builder{}

	for idx, char := range key {
		consumed := false
		last := idx == (len(key) - 1)

		if char == sep {
			resKeys = append(resKeys, currentKey.String())
			consumed = true
		}

		currentKey.WriteRune(char)

		if last && !consumed {
			resKeys = append(resKeys, currentKey.String())
		}
	}
	resKeys[0] = "/"

	return resKeys, nil
}

// Register registers a watcher for a given key
func (wd *watchDispatcher) Register(key string, w *watcher) {
	existingWatchers, ok := wd.watchesByKey.Load(key)
	if ok {
		existingWatchers = append(existingWatchers, w)
		wd.watchesByKey.Store(key, existingWatchers)
	} else {
		wd.watchesByKey.Store(key, watchersList{w})
	}
}

// Added dispatches an ADDED event to appropriate watchers
func (wd *watchDispatcher) Added(key string, obj runtime.Object) {
	wd.notify(key, watch.Added, obj)
}

// Deleted dispatches a DELETED event to appropriate watchers
func (wd *watchDispatcher) Deleted(key string, obj runtime.Object) {
	wd.notify(key, watch.Deleted, obj)
}

func (wd *watchDispatcher) notify(key string, eventType watch.EventType, obj runtime.Object) {
	// Don’t block callers by publishing in a separate goroutine
	go func() {
		event := watch.Event{Type: eventType, Object: obj}
		keysToNotify, err := exractKeysToNotify(key)
		if err != nil {
			return
		}

		for idx := range keysToNotify {
			notifiedKey := keysToNotify[idx]
			watchers, ok := wd.watchesByKey.Load(notifiedKey)
			if !ok {
				continue
			}

			for idx := range watchers {
				watchers[idx].notify(event)
			}
		}
	}()
}
