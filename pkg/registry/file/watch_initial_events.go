package file

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
)

// initialEvents is what a watch with sendInitialEvents ships before live events: the
// objects under the key as Added events, then the initial-events-end bookmark when the
// client allows bookmarks. listed keeps the resourceVersion each listed object had so a
// live event buffered while listing is not shipped a second time.
type initialEvents struct {
	events []watch.Event
	listed map[string]string
}

func wantsInitialEvents(opts storage.ListOptions) bool {
	return opts.SendInitialEvents != nil && *opts.SendInitialEvents
}

// covers reports whether the initial list already holds the state carried by ev.
func (ie initialEvents) covers(ev watch.Event) bool {
	if ev.Type != watch.Added && ev.Type != watch.Modified {
		return false
	}
	acc, err := meta.Accessor(ev.Object)
	if err != nil {
		return false
	}
	rv, ok := ie.listed[listedKey(acc)]
	return ok && rv == acc.GetResourceVersion()
}

func listedKey(acc metav1.Object) string {
	return acc.GetNamespace() + "/" + acc.GetName()
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
		obj := itemObj.DeepCopyObject()
		if err := lister.Get(ctx, key, storage.GetOptions{IgnoreNotFound: true, ResourceVersion: opts.ResourceVersion}, obj); err != nil {
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
	if err := meta.SetList(listObj, items); err != nil {
		return initialEvents{}, err
	}
	bookmark, err := meta.Accessor(itemObj)
	if err != nil {
		return initialEvents{}, err
	}
	bookmark.SetResourceVersion(computeListResourceVersion(listObj))
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
	_, group, resource, _, _, _ := K8sPathToKeys(key)
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
