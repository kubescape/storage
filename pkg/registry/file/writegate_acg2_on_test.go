package file

// AC-G2(on): the flag-on topology (config.ContainerProfileSqliteBackend) —
// the ObjectStore and the legacy StorageImpl over one pool, the legacy
// instance carrying the kind-ownership guard, one write gate. The lock
// holder is the gate itself (CR-3). Repair readers must queue behind an
// explicitly held gate and remain pending until release. Non-repair readers
// must complete before release. The armed AC-G1 check rejects ungated writes.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
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
	gate, err := newWriteGate(e.ctx, pool)
	require.NoError(t, err)
	e.gate = gate
	e.legacy.SetWriteGate(gate)
	e.cleanup.SetWriteGate(gate)
	store, err := NewObjectStore(pool, dbPath, e.legacy.watchDispatcher, e.legacy.scheme, e.processor, e.legacy, gate, ObjectStoreOptions{CheckpointInterval: time.Hour})
	require.NoError(t, err)
	e.store = store
	e.closeStore = func() {
		require.NoError(t, store.Close())
		require.NoError(t, gate.Close())
	}
	e.hold = func() func() {
		held := make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- e.gate.run(e.ctx, priorityHigh, "acg2-holder", "test", func(context.Context, *sqlite.Conn) error {
				close(held)
				select {
				case <-release:
					return nil
				case <-e.ctx.Done():
					return e.ctx.Err()
				}
			})
		}()
		select {
		case <-held:
		case <-time.After(acg2Deadline):
			close(release)
			t.Fatal("gate holder did not acquire the gate")
		}
		return sync.OnceFunc(func() {
			close(release)
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(acg2Deadline):
				t.Fatal("gate holder did not finish")
			}
		})
	}

	return e
}

var acg2On = acg2Topology{
	name:  "on",
	on:    true,
	build: newACG2OnEnv,
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
		key: func(e *acg2Env, cell string) string {
			p := acg2BaseProfile(e.t)
			// Include the state marker in the image-derived SBOM path so the
			// migration fixture distinguishes tool failure from success.
			p.Spec.ImageTag += "-" + cell
			e.preSaveProfile = p
			return acg2SbomKeyOf(e, p)
		},
		obj: newSBOM,
		prepare: func(e *acg2Env, key string) func() error {
			p := e.preSaveProfile
			return func() error {
				ctx, cleanup, err := e.processor.ContainerProfileStorage.WithConnection(e.ctx)
				if err != nil {
					return err
				}
				defer cleanup()
				return e.processor.PreSave(ctx, p)
			}
		}},
}

// TestACG2_FlagOn is AC-G2(on).
func TestACG2_FlagOn(t *testing.T) {
	runACG2Matrix(t, acg2On, append(append([]acg2Reader(nil), acg2Readers...), acg2OnReaders...))
}

var _ runtime.Object
