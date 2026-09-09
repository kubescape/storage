package file

// AC-G2(on): the flag-on topology (config.ContainerProfileSqliteBackend) —
// the ObjectStore and the legacy StorageImpl over one pool, the legacy
// instance carrying the kind-ownership guard, one write gate. The lock
// holder is the gate itself (CR-3): a gate.run whose fn holds for
// acg2HolderHold. A pool-connection BEGIN IMMEDIATE holder here would itself
// be an ungated writer the gate's own BEGIN IMMEDIATE legitimately
// busy-waits behind, and no repair cell could pass by construction.
//
// Bounds: non-repair cells return inside the hold (WAL readers never block
// on the writer); repair cells complete within holderHold + queue + margin,
// inside the 500 ms headline — a repair is a queued write, waiting one hold
// behind the gate instead of polling the busy handler. The armed AC-G1 check
// on every cell's pool is what turns an ungated repair into a failure.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/armosec/armoapi-go/armotypes"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/k8s-interface/names"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"zombiezen.com/go/sqlite"
)

// acg2HolderHold is how long the gate holder keeps the write lock under
// flag-on: long enough that a poller would be caught (its first wake-ups are
// 1…100 ms apart), short enough that hold + queue + the read stays inside
// acg2Prompt.
const acg2HolderHold = 250 * time.Millisecond

// newACG2OnEnv builds the flag-on environment: pool size ≥ 2 (R-9: the
// reader holds its pool connection while queued on the gate; the gate's
// dedicated connection is the other), the legacy instance with the guard,
// the ObjectStore, the cleanup handler, the fixture handle, and AC-G1 armed.
func newACG2OnEnv(t *testing.T) *acg2Env {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "acg2.sq3")
	rec := &tableRecorder{}
	pool := NewPoolWithOptions(dbPath, PoolOptions{Size: 4, BusyTimeout: acg2BusyTimeout, DisableAutoCheckpoint: true, Authorizer: rec.authorizer})
	armUngatedWriteCheck(t, pool)
	e := newACG2Base(t, pool, dbPath)
	t.Cleanup(e.closePool)
	e.rec = rec
	e.legacy.SetForeignKinds(IsContainerProfileKind)
	store, err := NewObjectStore(pool, dbPath, e.legacy.watchDispatcher, e.legacy.scheme, e.processor, e.legacy, ObjectStoreOptions{CheckpointInterval: time.Hour})
	require.NoError(t, err)
	e.store = store
	e.gate = store.gate
	e.closeStore = func() { require.NoError(t, store.Close()) }
	e.hold = func() func() {
		held := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = e.gate.run(e.ctx, priorityHigh, "acg2-holder", func(*sqlite.Conn) error {
				close(held)
				time.Sleep(acg2HolderHold)
				return nil
			})
		}()
		<-held
		return func() { <-done }
	}
	return e
}

var acg2On = acg2Topology{
	name:  "on",
	on:    true,
	build: newACG2OnEnv,
	bounds: func(repair bool) (time.Duration, time.Duration) {
		if repair {
			return acg2HolderHold, acg2Prompt
		}
		return 0, acg2HolderHold
	},
}

// acg2BaseProfile is testdata/p1.json as a base (consolidated) profile: the
// object PreSave runs the SBOM lookup for.
func acg2BaseProfile(t *testing.T) *softwarecomposition.ContainerProfile {
	t.Helper()
	content, err := os.ReadFile("testdata/p1.json")
	require.NoError(t, err)
	var p softwarecomposition.ContainerProfile
	require.NoError(t, json.Unmarshal(content, &p))
	p.Name, _ = SplitProfileName(p.Name)
	delete(p.Annotations, helpersv1.ReportSeriesIdMetadataKey)
	return &p
}

// acg2SbomKeyOf derives the sbomsyft key PreSave reads for p, exactly as
// ContainerProfileProcessor.PreSave does.
func acg2SbomKeyOf(e *acg2Env, p *softwarecomposition.ContainerProfile) string {
	e.t.Helper()
	slug, err := names.ImageInfoToSlug(p.Spec.ImageTag, p.Spec.ImageID)
	require.NoError(e.t, err)
	id := armotypes.ProfileIdentifier{
		ProfileScope: armotypes.ProfileScope{
			HostType:               e.processor.HostType,
			Cluster:                p.Annotations[helpersv1.ClusterMetadataKey],
			Namespace:              e.processor.DefaultNamespace,
			CloudAccountIdentifier: p.Annotations[helpersv1.CloudAccountIdentifierMetadataKey],
			Region:                 p.Annotations[helpersv1.RegionMetadataKey],
			HostID:                 p.Annotations[helpersv1.HostIDMetadataKey],
		},
		Name: slug,
	}
	return BuildContainerProfileKey(id, "sbomsyft")
}

// acg2OnReaders are the flag-on-only entry points: PreSave for a base CP,
// the path bug 2 sat on (PreSave → GetSbom → legacy GetWithConn → get()).
var acg2OnReaders = []acg2Reader{
	{name: "PreSave(base-CP)", class: acg2GetReader, onOnly: true,
		key: func(e *acg2Env, _ string) string { return acg2SbomKeyOf(e, acg2BaseProfile(e.t)) },
		obj: newSBOM,
		run: func(e *acg2Env, key string) error {
			p := acg2BaseProfile(e.t)
			ctx, cleanup, err := e.processor.ContainerProfileStorage.WithConnection(e.ctx)
			if err != nil {
				return err
			}
			defer cleanup()
			return e.processor.PreSave(ctx, p)
		}},
}

// TestACG2_FlagOn is AC-G2(on).
func TestACG2_FlagOn(t *testing.T) {
	runACG2Matrix(t, acg2On, append(append([]acg2Reader(nil), acg2Readers...), acg2OnReaders...))
}

var _ = context.Background
var _ runtime.Object
