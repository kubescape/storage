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

type watchRouter map[string][]*watcher

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

func (wr watchRouter) register(key string, w *watcher) {
	existingWatchers, ok := wr[key]
	if ok {
		wr[key] = append(existingWatchers, w)
	} else {
		wr[key] = []*watcher{w}
	}
}

func (wr watchRouter) notify(key string, eventType watch.EventType, obj runtime.Object) {
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
