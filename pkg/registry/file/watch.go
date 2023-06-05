package file

import (
	"errors"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/klog/v2"
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

// watchDispatcher dispatches events to registered watches
type watchDispatcher map[string][]*watcher

func newWatchDispatcher() watchDispatcher {
	return watchDispatcher{}
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

func (wr watchDispatcher) register(key string, w *watcher) {
	existingWatchers, ok := wr[key]
	if ok {
		wr[key] = append(existingWatchers, w)
	} else {
		wr[key] = []*watcher{w}
	}
}

func (wr watchDispatcher) notify(key string, eventType watch.EventType, obj runtime.Object) {
	// Don’t block callers by publishing in a separate goroutine
	go func() {
		klog.Warningf("Incoming key: %v", key)
		klog.Warningf("Current routes: %v", wr)

		event := watch.Event{Type: eventType, Object: obj}
		keysToNotify, err := exractKeysToNotify(key)
		klog.Warningf("Got keys: %v", keysToNotify)

		if err != nil {
			return
		}

		for idx := range keysToNotify {
			klog.Warningf("Notifying key: %v", keysToNotify[idx])
			notifiedKey := keysToNotify[idx]
			watchers, ok := wr[notifiedKey]
			if ok {
				for idx := range watchers {
					watchers[idx].notify(event)
				}
			} else {
				klog.Warningf("SKipping missing key")
			}
		}
	}()
}
