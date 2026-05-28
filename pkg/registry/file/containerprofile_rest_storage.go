package file

import (
	"context"
	"fmt"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
)

// ContainerProfileRESTStorage wraps the generic file-backed storage with a
// merged-first read path for ContainerProfile. The consolidator continues to
// read and write the canonical observed CP through the lower-level
// ContainerProfileStorage interface; this wrapper only changes what apiserver
// consumers (notably node-agent) see when they GET a ContainerProfile.
//
// Read path:
//  1. Try the parallel containerprofile-merged key first. If a merged artifact
//     exists, return it — it already embeds observed + ug- overlay.
//  2. On not-found, fall back to the canonical containerprofile key (the
//     observed CP). This keeps the GET contract stable for workloads with no
//     ug- AP/NN.
//
// Writes / List / Watch / Delete / GuaranteedUpdate pass straight through to
// the canonical key. The merged artifact is exclusively maintained by the
// consolidator (refreshMergedProfile), never by REST clients.
type ContainerProfileRESTStorage struct {
	realStore StorageQuerier
}

var _ storage.Interface = (*ContainerProfileRESTStorage)(nil)

// NewContainerProfileRESTStorage wraps realStore with merged-first Get
// semantics for ContainerProfile resources.
func NewContainerProfileRESTStorage(realStore StorageQuerier) storage.Interface {
	return &ContainerProfileRESTStorage{realStore: realStore}
}

func (c ContainerProfileRESTStorage) EnableResourceSizeEstimation(keysFunc storage.KeysFunc) error {
	return nil
}

func (c ContainerProfileRESTStorage) Versioner() storage.Versioner {
	return c.realStore.Versioner()
}

func (c ContainerProfileRESTStorage) Create(ctx context.Context, key string, obj, out runtime.Object, ttl uint64) error {
	return c.realStore.Create(ctx, key, obj, out, ttl)
}

func (c ContainerProfileRESTStorage) Delete(ctx context.Context, key string, out runtime.Object, preconditions *storage.Preconditions, validateDeletion storage.ValidateObjectFunc, cachedExistingObject runtime.Object, opts storage.DeleteOptions) error {
	// Delete the merged sibling first (best-effort) so a deleted CP doesn't
	// leave a stale merged artifact that the next GET would surface.
	if _, ok := out.(*softwarecomposition.ContainerProfile); ok {
		_ = c.realStore.Delete(ctx, MergedKeyFor(key), &softwarecomposition.ContainerProfile{}, nil, storage.ValidateAllObjectFunc, nil, storage.DeleteOptions{})
	}
	return c.realStore.Delete(ctx, key, out, preconditions, validateDeletion, cachedExistingObject, opts)
}

func (c ContainerProfileRESTStorage) Watch(ctx context.Context, key string, opts storage.ListOptions) (watch.Interface, error) {
	return c.realStore.Watch(ctx, key, opts)
}

func (c ContainerProfileRESTStorage) Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	if _, ok := objPtr.(*softwarecomposition.ContainerProfile); !ok {
		// Defensive: this wrapper is registered for ContainerProfile only, but
		// don't surprise an unexpected caller with a kind-segment rewrite.
		return c.realStore.Get(ctx, key, opts, objPtr)
	}
	mergedKey := MergedKeyFor(key)
	if mergedKey != key {
		err := c.realStore.Get(ctx, mergedKey, opts, objPtr)
		if err == nil {
			return nil
		}
		if !storage.IsNotFound(err) {
			// Surface unexpected errors (lock timeouts, decode failures) rather
			// than silently falling back — a hard failure on the merged read
			// likely means storage is unhealthy, and serving observed instead
			// could mask the issue from clients.
			return err
		}
		// merged not found, fall through to observed
		if err := runtime.SetZeroValue(objPtr); err != nil {
			return fmt.Errorf("reset object before observed fallback: %w", err)
		}
	}
	return c.realStore.Get(ctx, key, opts, objPtr)
}

func (c ContainerProfileRESTStorage) GetList(ctx context.Context, key string, opts storage.ListOptions, listObj runtime.Object) error {
	// Listing currently returns the canonical observed CPs. Consumers that
	// need the merged view will call Get per-item, which this wrapper handles.
	// Scoping List to merged-first would require interleaving two kinds and
	// reconciling per-item; the maintainer's review explicitly asked for the
	// read-path fallback (step 4), not list rewriting.
	return c.realStore.GetList(ctx, key, opts, listObj)
}

func (c ContainerProfileRESTStorage) GuaranteedUpdate(ctx context.Context, key string, destination runtime.Object, ignoreNotFound bool, preconditions *storage.Preconditions, tryUpdate storage.UpdateFunc, cachedExistingObject runtime.Object) error {
	return c.realStore.GuaranteedUpdate(ctx, key, destination, ignoreNotFound, preconditions, tryUpdate, cachedExistingObject)
}

func (c ContainerProfileRESTStorage) ReadinessCheck() error {
	return c.realStore.ReadinessCheck()
}

func (c ContainerProfileRESTStorage) RequestWatchProgress(ctx context.Context) error {
	return c.realStore.RequestWatchProgress(ctx)
}

func (c ContainerProfileRESTStorage) GetCurrentResourceVersion(ctx context.Context) (uint64, error) {
	if rv, ok := any(c.realStore).(interface {
		GetCurrentResourceVersion(context.Context) (uint64, error)
	}); ok {
		return rv.GetCurrentResourceVersion(ctx)
	}
	return 0, nil
}

func (c ContainerProfileRESTStorage) Stats(ctx context.Context) (storage.Stats, error) {
	if s, ok := any(c.realStore).(interface {
		Stats(context.Context) (storage.Stats, error)
	}); ok {
		return s.Stats(ctx)
	}
	return storage.Stats{}, fmt.Errorf("unimplemented")
}

func (c ContainerProfileRESTStorage) SetKeysFunc(f storage.KeysFunc) {
	if k, ok := any(c.realStore).(interface{ SetKeysFunc(storage.KeysFunc) }); ok {
		k.SetKeysFunc(f)
	}
}

func (c ContainerProfileRESTStorage) CompactRevision() int64 {
	if r, ok := any(c.realStore).(interface{ CompactRevision() int64 }); ok {
		return r.CompactRevision()
	}
	return 0
}
