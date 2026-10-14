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
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
)

// initialEvents is what a watch with sendInitialEvents ships before live events: the
// objects under the key as Added events, then the initial-events-end bookmark when the
// client allows bookmarks. listed keeps the resourceVersion of every listed object so the
// live events buffered while the list ran can be reconciled against that snapshot.
type initialEvents struct {
	events []watch.Event
	listed map[string]string
}

func wantsInitialEvents(opts storage.ListOptions) bool {
	return opts.SendInitialEvents != nil && *opts.SendInitialEvents
}

// reconcile returns the buffered live events the snapshot does not already represent, in
// order. The snapshot read of each object happened at some point of its buffered history:
// the last Added or Modified carrying the listed resourceVersion marks that point when the
// object was listed, the last Deleted marks it when it was not, and everything up to that
// point is already in the snapshot. Without such a mark, an Added or Modified of a listed
// object at or below the listed version is an older state the list has superseded.
func (ie initialEvents) reconcile(buffered []watch.Event) []watch.Event {
	cut := make(map[string]int, len(buffered))
	for i, ev := range buffered {
		key, rv, ok := eventKey(ev)
		if !ok {
			continue
		}
		listedRV, listed := ie.listed[key]
		switch {
		case listed && (ev.Type == watch.Added || ev.Type == watch.Modified) && rv == listedRV:
			cut[key] = i
		case !listed && ev.Type == watch.Deleted:
			cut[key] = i
		}
	}
	kept := make([]watch.Event, 0, len(buffered))
	for i, ev := range buffered {
		key, rv, ok := eventKey(ev)
		if !ok {
			kept = append(kept, ev)
			continue
		}
		if c, found := cut[key]; found {
			if i > c {
				kept = append(kept, ev)
			}
			continue
		}
		if listedRV, listed := ie.listed[key]; listed && (ev.Type == watch.Added || ev.Type == watch.Modified) && !newerThan(rv, listedRV) {
			continue
		}
		kept = append(kept, ev)
	}
	return kept
}

func eventKey(ev watch.Event) (key, rv string, ok bool) {
	acc, err := meta.Accessor(ev.Object)
	if err != nil {
		return "", "", false
	}
	return listedKey(acc), acc.GetResourceVersion(), true
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

func listedKey(acc metav1.Object) string {
	return acc.GetNamespace() + "/" + acc.GetName()
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

	snapshotRV := computeListResourceVersion(listObj)
	if minimum, ok := requestedMinimum(opts.ResourceVersion); ok {
		current, _ := strconv.ParseUint(snapshotRV, 10, 64)
		if current < minimum {
			return initialEvents{}, storage.NewTooLargeResourceVersionError(minimum, current, 1)
		}
	}

	ie := initialEvents{events: make([]watch.Event, 0, len(items)+1), listed: make(map[string]string, len(items))}
	for _, item := range items {
		acc, err := meta.Accessor(item)
		if err != nil {
			return initialEvents{}, err
		}
		ie.listed[listedKey(acc)] = acc.GetResourceVersion()
		ie.events = append(ie.events, watch.Event{Type: watch.Added, Object: item})
	}
	if !predicate.AllowWatchBookmarks {
		return ie, nil
	}
	bookmark, err := meta.Accessor(itemObj)
	if err != nil {
		return initialEvents{}, err
	}
	bookmark.SetResourceVersion(snapshotRV)
	if err := storage.AnnotateInitialEventsEndBookmark(itemObj); err != nil {
		return initialEvents{}, err
	}
	ie.events = append(ie.events, watch.Event{Type: watch.Bookmark, Object: itemObj})
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
