package file

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
)

// initialEvents is what a watch with sendInitialEvents ships before live events: the
// objects under the key as Added events, the live events buffered while the list ran
// reconciled against that snapshot, and then the initial-events-end bookmark when the
// client allows bookmarks. listed keeps, per object, the incarnation and version the
// snapshot holds.
type initialEvents struct {
	events   []watch.Event
	listed   map[string]listedObject
	bookmark runtime.Object
	rv       uint64
}

// listedObject is what the snapshot knows about one object: which incarnation (uid and
// creation time, since versions restart at every creation) and at which version.
type listedObject struct {
	uid     types.UID
	created metav1.Time
	rv      string
}

type eventIdentity struct {
	listedObject
	key string
}

func identify(ev watch.Event) (eventIdentity, bool) {
	acc, err := meta.Accessor(ev.Object)
	if err != nil {
		return eventIdentity{}, false
	}
	return eventIdentity{
		listedObject: listedObject{uid: acc.GetUID(), created: acc.GetCreationTimestamp(), rv: acc.GetResourceVersion()},
		key:          listedKey(acc),
	}, true
}

func listedKey(acc metav1.Object) string {
	return acc.GetNamespace() + "/" + acc.GetName()
}

// incarnation orders an event's object against the listed one: the same object, an
// earlier incarnation of the name, or a later one. Different uids are ordered by creation
// time; a tie counts as later so the event ships rather than being dropped.
type incarnation int

const (
	sameIncarnation incarnation = iota
	earlierIncarnation
	laterIncarnation
)

func (snap listedObject) incarnationOf(ev eventIdentity) incarnation {
	if ev.uid == snap.uid {
		return sameIncarnation
	}
	if ev.created.Before(&snap.created) {
		return earlierIncarnation
	}
	return laterIncarnation
}

func wantsInitialEvents(opts storage.ListOptions) bool {
	return opts.SendInitialEvents != nil && *opts.SendInitialEvents
}

// reconcile returns the buffered live events the snapshot does not already represent, in
// order. The snapshot read of each object happened at some point of its buffered history:
// the last Added or Modified of the listed incarnation at the listed version marks that
// point when the object was listed, the last Deleted marks it when it was not, and
// everything up to the mark is already in the snapshot. Without a mark, the history of an
// earlier incarnation of a listed name is superseded, and so is an Added or Modified of the
// listed incarnation at or below the listed version.
func (ie initialEvents) reconcile(buffered []watch.Event) []watch.Event {
	cut := make(map[string]int, len(buffered))
	for i, ev := range buffered {
		id, ok := identify(ev)
		if !ok {
			continue
		}
		snap, listed := ie.listed[id.key]
		switch {
		case listed && isUpsert(ev) && snap.incarnationOf(id) == sameIncarnation && id.rv == snap.rv:
			cut[id.key] = i
		case !listed && ev.Type == watch.Deleted:
			cut[id.key] = i
		}
	}
	kept := make([]watch.Event, 0, len(buffered))
	for i, ev := range buffered {
		id, ok := identify(ev)
		if !ok {
			kept = append(kept, ev)
			continue
		}
		if c, found := cut[id.key]; found {
			if i > c {
				kept = append(kept, ev)
			}
			continue
		}
		if snap, listed := ie.listed[id.key]; listed {
			switch snap.incarnationOf(id) {
			case earlierIncarnation:
				continue
			case sameIncarnation:
				if isUpsert(ev) && !newerThan(id.rv, snap.rv) {
					continue
				}
			}
		}
		kept = append(kept, ev)
	}
	return kept
}

func isUpsert(ev watch.Event) bool {
	return ev.Type == watch.Added || ev.Type == watch.Modified
}

// newerThan reports whether rv is a numeric resourceVersion above listed. Versions that do
// not parse count as newer, so the event is shipped rather than dropped.
func newerThan(rv, listed string) bool {
	a, errA := strconv.ParseUint(rv, 10, 64)
	b, errB := strconv.ParseUint(listed, 10, 64)
	if errA != nil || errB != nil {
		return true
	}
	return a > b
}

// bookmarkEvent returns the initial-events-end bookmark for the state the client holds
// after the reconciled events, or false when the client did not ask for bookmarks.
func (ie initialEvents) bookmarkEvent(shipped []watch.Event) (watch.Event, bool) {
	if ie.bookmark == nil {
		return watch.Event{}, false
	}
	rv := ie.rv
	for _, ev := range shipped {
		if !isUpsert(ev) {
			continue
		}
		if id, ok := identify(ev); ok {
			if v, err := strconv.ParseUint(id.rv, 10, 64); err == nil && v > rv {
				rv = v
			}
		}
	}
	acc, err := meta.Accessor(ie.bookmark)
	if err != nil {
		return watch.Event{}, false
	}
	acc.SetResourceVersion(strconv.FormatUint(rv, 10))
	return watch.Event{Type: watch.Bookmark, Object: ie.bookmark}, true
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
		// A named watch reads like the list it stands in for: metadata only, unless the
		// full spec was asked for explicitly.
		readRV := softwarecomposition.ResourceVersionMetadata
		if opts.ResourceVersion == softwarecomposition.ResourceVersionFullSpec {
			readRV = ""
		}
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

	ie := initialEvents{events: make([]watch.Event, 0, len(items)), listed: make(map[string]listedObject, len(items)), rv: snapshotRV}
	for _, item := range items {
		acc, err := meta.Accessor(item)
		if err != nil {
			return initialEvents{}, err
		}
		ie.listed[listedKey(acc)] = listedObject{uid: acc.GetUID(), created: acc.GetCreationTimestamp(), rv: acc.GetResourceVersion()}
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
