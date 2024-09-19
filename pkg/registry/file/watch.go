package file

import (
	"context"
	"path"
	"slices"

	"github.com/puzpuzpuz/xsync/v2"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
)

/*
watcher receives and forwards events to its listeners.

In particular, it implements the watch.Interface. But what does that mean?
The formal sense is easy: Implement the Step and ResultChan method in the right format.
The semantics however are something much more convoluted.
For example:
  - Can you call Stop multiple times? What should happen then?
    (The implementation used to crash here)
  - What should happen Stop to happened events whose Results are not retrieved yet?
    (The implementation used to sometimes drop them, sometimes deadlock both the Stopper
    and the watcher, leaking also goroutines and memory.)
  - What should the behavior be if client does not immediately read from the ResultChan() ?
    (The implementation used to sometimes queue them into the stack of new goroutines, yet
    sometimes block other notications against the same event/key until processed)

The API doc (apimachinery/pkg/watch.Interface) says:
1 We shouldn't leak resources after Stop (goroutines, memory).
2 ResultChan receives all events "happened before" Stop() (what that means exactly, TBD).
3 ResultChan should be closed eventually after Stop() call.

The actual usage of the API implies:

	4 Stop() can be called multiple times.
	5 Stop() can also be called when the queue is not empty.
	6 Queue might also not be emptied by client.
	7 The queue of the watcher is not necessarily being read all the time
	  (for some values of "all").

Following the Hyrum's Law, this shall be the implicit interface to write the watcher against.

How to implement this?
Problem with #3 is that typically closing the channel is used by the sender to tell that the receiver
can stop; here the role is inverted, making the control flow supported naturally by the primitives
working against us. Best long term choice would be to change the API Doc, but let's try to accommodate
instead.

Constraint #7 is particularly challenging as well. We have only bad options.
Basically the underlying issue is that the server-side has to implement the queue management strategy,
yet client has full control of what kind of and how they are going to use the queue. Options:
a) "No queue" on server-side (there's always a queue). I.e. your queue is pushed outside the server
by causing backpressure by halting your server processing. The upside is that this is easiest to implement
and follows the spec. Unfortunately the server-side performance is going to be particularly miserable.
b) No queue, but fall back to dropping messages. This breakes the constraint #2 though.
c) Fixed queue, fall back to a or b when queue gets full.
d) Infinite queue. Unfortunately, only pure turing machines have those. Trying to construct one

	leads to solution (c_a) anyways, but with less predictable collapses and pushback.

Ok. So the challenge with all the variants API-wise is that we have no way of communicating that we are
backlogged or that we are dropping messages.
Let's choose one. The c_a seems like the path of least suprise (c_b is possible as well).
*/
type watcher struct {
	ctx         context.Context
	stop        context.CancelFunc
	outCh, inCh chan watch.Event
}

// newWatcher creates a new watcher
func newWatcher(ctx context.Context) *watcher {
	ctx, cn := context.WithCancel(ctx)
	w := &watcher{
		ctx:   ctx,
		stop:  cn,
		outCh: make(chan watch.Event, 100),
		inCh:  make(chan watch.Event),
	}
	go w.shipIt()
	return w
}

func (w *watcher) Stop()                          { w.stop() }
func (w *watcher) ResultChan() <-chan watch.Event { return w.outCh }

// shipIt is the only method writing to outCh. It is called only once per watcher
// and returns when context is gone.
// See discussion on constraint #3 above for rationale for this approach.
func (w *watcher) shipIt() {
	defer close(w.outCh)
	for {
		var ev watch.Event
		select { // we want both reads and writes to be interruptable, hence complexity here.
		case <-w.ctx.Done():
			return
		case ev = <-w.inCh:
		}
		select {
		case <-w.ctx.Done():
			return
		case w.outCh <- ev:
		}
	}
}

func (w *watcher) notify(e watch.Event) {
	select {
	case w.inCh <- e:
	case <-w.ctx.Done():
	}
}

type watchersList []*watcher

// watchDispatcher dispatches events to registered watches
//
// TODO(ttimonen): There's currently no way to gracefully take down watchDispatcher without leaking a goroutine.
type watchDispatcher struct {
	watchesByKey *xsync.MapOf[string, watchersList]
	gcCh         chan string
}

func newWatchDispatcher() *watchDispatcher {
	wd := watchDispatcher{xsync.NewMapOf[watchersList](), make(chan string)}
	go wd.gcer()
	return &wd
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
	go func() {
		<-w.ctx.Done()
		wd.gcCh <- key
	}()
}

func (wd *watchDispatcher) gcer() {
	for key := range wd.gcCh { // This is an O(n) op, where n is # of watchers in a particular key.
		wd.watchesByKey.Compute(key, func(l watchersList, _ bool) (watchersList, bool) {
			if len(l) == 0 {
				return nil, true
			}
			out := make(watchersList, 0, len(l)-1) // Preallocate with the intent to drop one element
			// Doing dropping inplace would be more efficient alloc-wise, but would cause data races on notify
			// the way it's currently implemented.
			for _, w := range l {
				if w.ctx.Err() == nil {
					out = append(out, w)
				}
			}
			return out, len(out) == 0
		})
		// TODO(ttimonen) sleeping a bit here can give a batch cleanup improvements, maybe.
	}
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
	// Notify calls do not block normally, unless the client-side is messed up.
	event := watch.Event{Type: eventType, Object: obj}
	for _, part := range extractKeysToNotify(key) {
		ws, _ := wd.watchesByKey.Load(part)
		for _, w := range ws {
			w.notify(event)
		}
	}
}
