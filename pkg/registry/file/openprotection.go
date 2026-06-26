package file

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/armosec/armoapi-go/armotypes"
	"github.com/kubescape/go-logger"
	loggerhelpers "github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// OpenProtectionConfigMapKey is the key under which the open-protection union
// (JSON-encoded armotypes.OpenMatchers) is stored in the source ConfigMap.
const OpenProtectionConfigMapKey = "openProtection"

// DefaultOpenProtectionRefreshInterval is how often the reloader re-reads the
// source ConfigMap when no interval is configured. Rule bindings change rarely,
// so a coarse interval keeps API-server load negligible while still picking up
// operator-published changes within about a minute.
const DefaultOpenProtectionRefreshInterval = time.Minute

// OpenProtectionStore is a concurrency-safe holder for the active open-protection
// matchers. The container-profile processor reads it on every PreSave (the open-
// event hot path, by far the most common) via Get, while a reloader goroutine
// swaps it whenever the source ConfigMap changes via Set. Reads take a read-lock
// and copy the small value, so profile deflation never races a refresh.
type OpenProtectionStore struct {
	mu      sync.RWMutex
	current dynamicpathdetector.OpenProtection
}

// NewOpenProtectionStore seeds a store with the initial matchers (e.g. a static
// value from config used until the first successful ConfigMap read, or the sole
// value in environments without a reloader).
func NewOpenProtectionStore(initial armotypes.OpenMatchers) *OpenProtectionStore {
	return &OpenProtectionStore{
		current: OpenProtectionFromMatchers(initial),
	}
}

// Get returns the current protection. The returned value shares the underlying
// slices with the stored value; callers must treat it as read-only. Profile
// deflation only ranges over the slices, so this is safe and allocation-free on
// the hot path.
func (s *OpenProtectionStore) Get() dynamicpathdetector.OpenProtection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// Set replaces the current protection with the union derived from m. Set is
// called by the reloader (rarely); Get is called on the hot path (often), which
// is why the lock favours readers.
func (s *OpenProtectionStore) Set(m armotypes.OpenMatchers) {
	p := OpenProtectionFromMatchers(m)
	s.mu.Lock()
	s.current = p
	s.mu.Unlock()
}

// ParseOpenProtectionConfigMap extracts the open-protection union from a
// ConfigMap's data. The union is stored as JSON under OpenProtectionConfigMapKey.
// A missing or empty key yields an empty (legacy, no-protection) union without
// error, so the producer can clear protection by removing the key.
func ParseOpenProtectionConfigMap(data map[string]string) (armotypes.OpenMatchers, error) {
	raw, ok := data[OpenProtectionConfigMapKey]
	if !ok || raw == "" {
		return armotypes.OpenMatchers{}, nil
	}
	var m armotypes.OpenMatchers
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return armotypes.OpenMatchers{}, fmt.Errorf("parse open-protection configmap key %q: %w", OpenProtectionConfigMapKey, err)
	}
	return m, nil
}

// OpenProtectionReloader periodically reads the open-protection ConfigMap and
// updates the shared store, so the storage apiserver tracks the set of sensitive
// prefixes published by the operator (the union of active rules'
// profileDataRequired.opens) without a restart.
//
// This is the in-cluster reader side of the "operator writes one object, storage
// refreshes periodically" wiring. The producer side — the operator watching
// RuntimeRuleAlertBinding, resolving selectors against the rule library, and
// writing this ConfigMap — is implemented separately. The reader tolerates the
// ConfigMap being absent (operator not yet deployed) by keeping the current
// protection rather than wiping it, which avoids a transient unprotection window
// and errs toward keeping sensitive paths detectable.
type OpenProtectionReloader struct {
	client    kubernetes.Interface
	namespace string
	name      string
	interval  time.Duration
	store     *OpenProtectionStore
}

// NewOpenProtectionReloader builds a reloader for the ConfigMap namespace/name,
// refreshing into store every interval (a non-positive interval falls back to
// DefaultOpenProtectionRefreshInterval).
func NewOpenProtectionReloader(client kubernetes.Interface, namespace, name string, interval time.Duration, store *OpenProtectionStore) *OpenProtectionReloader {
	if interval <= 0 {
		interval = DefaultOpenProtectionRefreshInterval
	}
	return &OpenProtectionReloader{
		client:    client,
		namespace: namespace,
		name:      name,
		interval:  interval,
		store:     store,
	}
}

// reloadOnce reads the ConfigMap and applies it to the store. A NotFound is
// treated as "no source published yet" and keeps the current protection, so we
// never drop protection because the operator hasn't created the ConfigMap.
func (r *OpenProtectionReloader) reloadOnce(ctx context.Context) error {
	cm, err := r.client.CoreV1().ConfigMaps(r.namespace).Get(ctx, r.name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.L().Debug("open-protection configmap not found; keeping current protection",
				loggerhelpers.String("namespace", r.namespace),
				loggerhelpers.String("name", r.name))
			return nil
		}
		return err
	}
	m, err := ParseOpenProtectionConfigMap(cm.Data)
	if err != nil {
		return err
	}
	r.store.Set(m)
	return nil
}

// Run blocks, refreshing on each tick until ctx is cancelled. It performs an
// immediate initial read so the apiserver adopts the published protection
// without waiting a full interval.
func (r *OpenProtectionReloader) Run(ctx context.Context) {
	if err := r.reloadOnce(ctx); err != nil {
		logger.L().Ctx(ctx).Warning("open-protection initial reload failed", loggerhelpers.Error(err))
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.reloadOnce(ctx); err != nil {
				logger.L().Ctx(ctx).Warning("open-protection reload failed", loggerhelpers.Error(err))
			}
		}
	}
}
