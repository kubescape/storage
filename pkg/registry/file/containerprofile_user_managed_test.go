package file

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"sort"
	"testing"
	"time"

	"github.com/armosec/armoapi-go/armotypes"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/goradd/maps"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/kubescape/storage/pkg/utils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

// Tests for the user-managed (ug-) merge logic. The merge fans out across all
// three container slices and is additive: status/completion annotations are
// untouched, spec slices receive new entries, and matching PolicyByRuleId /
// NetworkNeighbor / NetworkPort entries are unioned by key.

func TestMergeUserAPIntoCP_ContainerSlicesAndPolicy(t *testing.T) {
	cp := &softwarecomposition.ContainerProfile{
		Spec: softwarecomposition.ContainerProfileSpec{
			Capabilities: []string{"NET_ADMIN"},
			Syscalls:     []string{"read"},
			PolicyByRuleId: map[string]softwarecomposition.RulePolicy{
				"R1": {AllowedProcesses: []string{"a"}},
			},
		},
	}
	userAP := &softwarecomposition.ApplicationProfile{
		Spec: softwarecomposition.ApplicationProfileSpec{
			Containers: []softwarecomposition.ApplicationProfileContainer{
				{
					Name:         "main",
					Capabilities: []string{"SYS_PTRACE"},
					Syscalls:     []string{"write"},
					PolicyByRuleId: map[string]softwarecomposition.RulePolicy{
						"R1": {AllowedProcesses: []string{"b"}},
						"R2": {AllowedContainer: true},
					},
				},
			},
			InitContainers: []softwarecomposition.ApplicationProfileContainer{
				{Name: "init", Capabilities: []string{"INIT_CAP"}},
			},
		},
	}

	mergeUserAPIntoCP(cp, userAP, "main")

	assert.ElementsMatch(t, []string{"NET_ADMIN", "SYS_PTRACE"}, cp.Spec.Capabilities)
	assert.ElementsMatch(t, []string{"read", "write"}, cp.Spec.Syscalls)
	r1Procs := cp.Spec.PolicyByRuleId["R1"].AllowedProcesses
	sort.Strings(r1Procs)
	assert.Equal(t, []string{"a", "b"}, r1Procs)
	assert.True(t, cp.Spec.PolicyByRuleId["R2"].AllowedContainer)
}

func TestMergeUserAPIntoCP_MatchesInitAndEphemeral(t *testing.T) {
	userAP := &softwarecomposition.ApplicationProfile{
		Spec: softwarecomposition.ApplicationProfileSpec{
			InitContainers: []softwarecomposition.ApplicationProfileContainer{
				{Name: "init", Capabilities: []string{"INIT_CAP"}},
			},
			EphemeralContainers: []softwarecomposition.ApplicationProfileContainer{
				{Name: "debug", Capabilities: []string{"DBG_CAP"}},
			},
		},
	}

	cpInit := &softwarecomposition.ContainerProfile{}
	mergeUserAPIntoCP(cpInit, userAP, "init")
	assert.Equal(t, []string{"INIT_CAP"}, cpInit.Spec.Capabilities)

	cpEph := &softwarecomposition.ContainerProfile{}
	mergeUserAPIntoCP(cpEph, userAP, "debug")
	assert.Equal(t, []string{"DBG_CAP"}, cpEph.Spec.Capabilities)
}

func TestMergeUserAPIntoCP_NoMatchIsNoOp(t *testing.T) {
	cp := &softwarecomposition.ContainerProfile{
		Spec: softwarecomposition.ContainerProfileSpec{Capabilities: []string{"X"}},
	}
	userAP := &softwarecomposition.ApplicationProfile{
		Spec: softwarecomposition.ApplicationProfileSpec{
			Containers: []softwarecomposition.ApplicationProfileContainer{
				{Name: "other", Capabilities: []string{"SHOULD_NOT_APPEAR"}},
			},
		},
	}
	mergeUserAPIntoCP(cp, userAP, "missing")
	assert.Equal(t, []string{"X"}, cp.Spec.Capabilities)
}

func TestMergeUserAPIntoCP_UserSlicesNotAliased(t *testing.T) {
	// Ensure the merge does not alias the caller's CRD slices. A subsequent
	// mutation on the user CRD must not bleed into the merged CP.
	userAP := &softwarecomposition.ApplicationProfile{
		Spec: softwarecomposition.ApplicationProfileSpec{
			Containers: []softwarecomposition.ApplicationProfileContainer{
				{Name: "main", Capabilities: []string{"A"}},
			},
		},
	}
	cp := &softwarecomposition.ContainerProfile{}
	mergeUserAPIntoCP(cp, userAP, "main")

	userAP.Spec.Containers[0].Capabilities[0] = "MUTATED"
	assert.Equal(t, []string{"A"}, cp.Spec.Capabilities)
}

func TestMergeUserNNIntoCP_IngressUnionByIdentifier(t *testing.T) {
	cp := &softwarecomposition.ContainerProfile{
		Spec: softwarecomposition.ContainerProfileSpec{
			Ingress: []softwarecomposition.NetworkNeighbor{
				{Identifier: "n1", DNSNames: []string{"a.example"}},
			},
		},
	}
	userNN := &softwarecomposition.NetworkNeighborhood{
		Spec: softwarecomposition.NetworkNeighborhoodSpec{
			Containers: []softwarecomposition.NetworkNeighborhoodContainer{
				{
					Name: "main",
					Ingress: []softwarecomposition.NetworkNeighbor{
						{Identifier: "n1", DNSNames: []string{"b.example"}},
						{Identifier: "n2", DNSNames: []string{"c.example"}},
					},
				},
			},
		},
	}
	mergeUserNNIntoCP(cp, userNN, "main")

	require.Len(t, cp.Spec.Ingress, 2)
	var n1 *softwarecomposition.NetworkNeighbor
	for i := range cp.Spec.Ingress {
		if cp.Spec.Ingress[i].Identifier == "n1" {
			n1 = &cp.Spec.Ingress[i]
		}
	}
	require.NotNil(t, n1)
	sort.Strings(n1.DNSNames)
	assert.Equal(t, []string{"a.example", "b.example"}, n1.DNSNames)
}

func TestMergeUserNNIntoCP_PortUserWinsOnCollision(t *testing.T) {
	port80 := int32(80)
	port8080 := int32(8080)
	cp := &softwarecomposition.ContainerProfile{
		Spec: softwarecomposition.ContainerProfileSpec{
			Ingress: []softwarecomposition.NetworkNeighbor{
				{
					Identifier: "n1",
					Ports: []softwarecomposition.NetworkPort{
						{Name: "tcp-80", Port: &port80},
					},
				},
			},
		},
	}
	userNN := &softwarecomposition.NetworkNeighborhood{
		Spec: softwarecomposition.NetworkNeighborhoodSpec{
			Containers: []softwarecomposition.NetworkNeighborhoodContainer{
				{
					Name: "main",
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							Identifier: "n1",
							Ports: []softwarecomposition.NetworkPort{
								{Name: "tcp-80", Port: &port8080}, // user wins
							},
						},
					},
				},
			},
		},
	}
	mergeUserNNIntoCP(cp, userNN, "main")

	require.Len(t, cp.Spec.Ingress, 1)
	require.Len(t, cp.Spec.Ingress[0].Ports, 1)
	require.NotNil(t, cp.Spec.Ingress[0].Ports[0].Port)
	assert.Equal(t, int32(8080), *cp.Spec.Ingress[0].Ports[0].Port)
}

func TestMergeUserNNIntoCP_LabelSelectorMerged(t *testing.T) {
	cp := &softwarecomposition.ContainerProfile{
		Spec: softwarecomposition.ContainerProfileSpec{
			LabelSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "x"},
			},
		},
	}
	userNN := &softwarecomposition.NetworkNeighborhood{
		Spec: softwarecomposition.NetworkNeighborhoodSpec{
			LabelSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"tier": "backend"},
			},
		},
	}
	mergeUserNNIntoCP(cp, userNN, "missing")
	assert.Equal(t, map[string]string{"app": "x", "tier": "backend"}, cp.Spec.LabelSelector.MatchLabels)
}

// End-to-end consolidation test plumbing.

// p1 / p2 fixtures both report time-series for container "coredns" of
// replicaset-coredns-5d78c9869d in namespace kube-system.
//
// Keys are derived at runtime via BuildContainerProfileKey rather than
// hand-written so a future change to the key format is caught here too.
const (
	e2eNS              = "kube-system"
	e2eWorkloadSlug    = "replicaset-coredns-5d78c9869d"
	e2eWorkloadUg      = "ug-" + e2eWorkloadSlug
	e2eContainerCPName = e2eWorkloadSlug + "-coredns-185f-129c"
)

func e2eUgAPKey() string {
	return BuildContainerProfileKey(armotypes.ProfileIdentifier{
		ProfileScope: armotypes.ProfileScope{HostType: armotypes.HostTypeKubernetes, Namespace: e2eNS},
		Name:         e2eWorkloadUg,
	}, "applicationprofiles")
}

func e2eUgNNKey() string {
	return BuildContainerProfileKey(armotypes.ProfileIdentifier{
		ProfileScope: armotypes.ProfileScope{HostType: armotypes.HostTypeKubernetes, Namespace: e2eNS},
		Name:         e2eWorkloadUg,
	}, "networkneighborhoods")
}

func e2eCPKey() string {
	return BuildContainerProfileKey(armotypes.ProfileIdentifier{
		ProfileScope: armotypes.ProfileScope{HostType: armotypes.HostTypeKubernetes, Namespace: e2eNS},
		Name:         e2eContainerCPName,
	}, "containerprofile")
}

func e2eMergedCPKey() string {
	return MergedKeyFor(e2eCPKey())
}

type e2eHarness struct {
	t         *testing.T
	pool      *sqlitemigration.Pool
	conn      *sqlite.Conn
	s         *StorageImpl
	processor *ContainerProfileProcessor
	ctx       context.Context
	cancel    context.CancelFunc
}

func newE2EHarness(t *testing.T) *e2eHarness {
	t.Helper()
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	conn, err := pool.Take(context.TODO())
	require.NoError(t, err)

	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	processor := &ContainerProfileProcessor{
		DeleteThreshold:         0,
		MaxContainerProfileSize: 40000,
		HostType:                armotypes.HostTypeKubernetes,
	}
	s := &StorageImpl{
		appFs:           afero.NewMemMapFs(),
		pool:            pool,
		locks:           utils.NewMapMutex[string](),
		processor:       processor,
		root:            DefaultStorageRoot,
		scheme:          sch,
		versioner:       storage.APIObjectVersioner{},
		watchDispatcher: NewWatchDispatcher(),
	}
	processor.SetStorage(NewContainerProfileStorageImpl(s, pool))

	ctx, cancel := context.WithTimeout(context.TODO(), 15*time.Second)
	return &e2eHarness{t: t, pool: pool, conn: conn, s: s, processor: processor, ctx: ctx, cancel: cancel}
}

func (h *e2eHarness) close() {
	h.cancel()
	h.pool.Put(h.conn)
	_ = h.pool.Close()
}

func (h *e2eHarness) createCP(fixture string) {
	h.t.Helper()
	content, err := os.ReadFile(fixture)
	require.NoError(h.t, err)
	var profile softwarecomposition.ContainerProfile
	require.NoError(h.t, json.Unmarshal(content, &profile))
	require.NoError(h.t, h.s.Create(h.ctx,
		"/spdx.softwarecomposition.kubescape.io/containerprofile/"+profile.Namespace+"/"+profile.Name,
		&profile, nil, 0))
}

// seedNonCP writes a non-ContainerProfile object via the storage layer, working
// around the test harness's processor wiring (production wires
// ContainerProfileProcessor only for the containerprofile kind via a per-kind
// registry; our test wires it for everything, and AfterCreate rejects non-CP).
func (h *e2eHarness) seedNonCP(key string, obj runtime.Object) {
	h.t.Helper()
	prev := h.s.processor
	h.s.processor = DefaultProcessor{}
	defer func() { h.s.processor = prev }()
	require.NoError(h.t, h.s.Create(h.ctx, key, obj, nil, 0))
}

// replaceUserAP swaps the spec of an existing ug- AP via GuaranteedUpdate so
// the versioner bumps the object's ResourceVersion (saveObject does
// existing.RV+1). This mirrors how a kube-apiserver-driven update lands in
// storage. A fresh Create after Delete would reset RV to 1, defeating the
// purpose of the RV-marker test.
func (h *e2eHarness) replaceUserAP(spec softwarecomposition.ApplicationProfileSpec) {
	h.t.Helper()
	prev := h.s.processor
	h.s.processor = DefaultProcessor{}
	defer func() { h.s.processor = prev }()

	tryUpdate := func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
		out := input.DeepCopyObject().(*softwarecomposition.ApplicationProfile)
		out.Spec = spec
		return out, nil, nil
	}
	require.NoError(h.t, h.s.GuaranteedUpdateWithConn(
		h.ctx, h.conn, e2eUgAPKey(), &softwarecomposition.ApplicationProfile{},
		false, nil, tryUpdate, nil, ""))
}

func (h *e2eHarness) consolidate() {
	h.t.Helper()
	require.NoError(h.t, h.processor.ConsolidateTimeSeries(h.ctx))
}

// watchMergedModifications registers a watcher for the merged-CP kind and
// returns a drain function that reports how many Modified events have arrived
// since the previous call. The exact merged key is namespaced (Watch rejects
// namespaced keys), so we register at the namespace-less kind path; the
// dispatcher fans events from the namespaced child up to parent-path watchers.
// A merged write only fires through StorageImpl.saveObject (the sole Modified
// emitter), so a count of zero is a reliable "the merged CP was not written".
func (h *e2eHarness) watchMergedModifications() (drain func() int, stop func()) {
	h.t.Helper()
	mergedKindPath := path.Dir(path.Dir(e2eMergedCPKey()))
	w, err := h.s.Watch(h.ctx, mergedKindPath, storage.ListOptions{})
	require.NoError(h.t, err)
	drain = func() int {
		n := 0
		for {
			select {
			case ev, ok := <-w.ResultChan():
				if !ok {
					return n
				}
				if ev.Type == watch.Modified {
					n++
				}
			case <-time.After(250 * time.Millisecond):
				return n
			}
		}
	}
	return drain, w.Stop
}

func (h *e2eHarness) loadConsolidated() softwarecomposition.ContainerProfile {
	h.t.Helper()
	var cp softwarecomposition.ContainerProfile
	require.NoError(h.t, h.s.GetWithConn(h.ctx, h.conn, e2eCPKey(), storage.GetOptions{}, &cp))
	return cp
}

// loadMerged reads the merged (effective) CP from its parallel key. Returns
// the profile and true on success, or a zero CP and false when no merged
// artifact exists for this workload (the legitimate "no ug- input" case).
func (h *e2eHarness) loadMerged() (softwarecomposition.ContainerProfile, bool) {
	h.t.Helper()
	var cp softwarecomposition.ContainerProfile
	err := h.s.GetWithConn(h.ctx, h.conn, e2eMergedCPKey(), storage.GetOptions{}, &cp)
	if err != nil {
		if storage.IsNotFound(err) {
			return softwarecomposition.ContainerProfile{}, false
		}
		require.NoError(h.t, err)
	}
	return cp, true
}

// requireMerged loads and requires the merged CP to exist; failing the test
// otherwise. Use this in tests asserting the merge fired.
func (h *e2eHarness) requireMerged() softwarecomposition.ContainerProfile {
	h.t.Helper()
	cp, ok := h.loadMerged()
	require.True(h.t, ok, "expected merged CP at %s to exist", e2eMergedCPKey())
	return cp
}

func count(s []string, v string) int {
	n := 0
	for _, x := range s {
		if x == v {
			n++
		}
	}
	return n
}

// TestConsolidateMergesUserManagedAP exercises the full consolidation flow
// with a ug-<workloadSlug> ApplicationProfile pre-seeded into storage.
func TestConsolidateMergesUserManagedAP(t *testing.T) {
	h := newE2EHarness(t)
	defer h.close()

	h.createCP("testdata/p1.json")

	userAP := &softwarecomposition.ApplicationProfile{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: e2eNS, Name: e2eWorkloadUg,
			Annotations: map[string]string{helpersv1.ManagedByMetadataKey: helpersv1.ManagedByUserValue},
		},
		Spec: softwarecomposition.ApplicationProfileSpec{
			Containers: []softwarecomposition.ApplicationProfileContainer{
				{Name: "coredns", Capabilities: []string{"USER_MANAGED_CAP"}, Syscalls: []string{"user_managed_syscall"}},
			},
		},
	}
	h.seedNonCP(e2eUgAPKey(), userAP)

	h.consolidate()

	// Observed must not carry user-managed entries — the canonical CP stays
	// pure time-series data (kubescape/storage#315 review).
	observed := h.loadConsolidated()
	assert.NotContains(t, observed.Spec.Capabilities, "USER_MANAGED_CAP",
		"observed CP must not be mutated by ug- merge")

	// Merged artifact carries the union plus provenance.
	merged := h.requireMerged()
	assert.Contains(t, merged.Spec.Capabilities, "USER_MANAGED_CAP")
	assert.Contains(t, merged.Spec.Syscalls, "user_managed_syscall")
	assert.Equal(t, 1, count(merged.Spec.Capabilities, "USER_MANAGED_CAP"))
	assert.Equal(t, MergedProfileLabelValue, merged.Labels[MergedProfileLabelKey])
	assert.NotEmpty(t, merged.Annotations[mergedSourceUserAPKey])
}

// TestConsolidateMergesUserManagedNN verifies a ug-<workloadSlug>
// NetworkNeighborhood is also merged into the consolidated CP, including
// container Ingress/Egress and the workload-level pod LabelSelector.
func TestConsolidateMergesUserManagedNN(t *testing.T) {
	h := newE2EHarness(t)
	defer h.close()

	h.createCP("testdata/p1.json")

	port443 := int32(443)
	userNN := &softwarecomposition.NetworkNeighborhood{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: e2eNS, Name: e2eWorkloadUg,
			Annotations: map[string]string{helpersv1.ManagedByMetadataKey: helpersv1.ManagedByUserValue},
		},
		Spec: softwarecomposition.NetworkNeighborhoodSpec{
			LabelSelector: metav1.LabelSelector{MatchLabels: map[string]string{"user-tier": "edge"}},
			Containers: []softwarecomposition.NetworkNeighborhoodContainer{
				{
					Name: "coredns",
					Egress: []softwarecomposition.NetworkNeighbor{
						{
							Identifier: "user-egress-1",
							DNSNames:   []string{"user.example"},
							Ports:      []softwarecomposition.NetworkPort{{Name: "tcp-443", Port: &port443}},
						},
					},
				},
			},
		},
	}
	h.seedNonCP(e2eUgNNKey(), userNN)

	h.consolidate()
	cp := h.requireMerged()

	require.NotEmpty(t, cp.Spec.Egress)
	found := false
	for _, e := range cp.Spec.Egress {
		if e.Identifier == "user-egress-1" {
			found = true
			assert.Contains(t, e.DNSNames, "user.example")
		}
	}
	assert.True(t, found, "expected user-managed egress neighbor in merged CP")
	assert.Equal(t, "edge", cp.Spec.LabelSelector.MatchLabels["user-tier"])
	assert.NotEmpty(t, cp.Annotations[mergedSourceUserNNKey])
}

// TestConsolidateUserManagedIdempotent verifies that re-merging unchanged
// inputs does NOT rewrite the merged CP, while a real change does.
//
// The merged CP is rebuilt from (observed, ug-AP, ug-NN) every tick. Because it
// is a DeepCopy of the observed CP it used to carry observed's ResourceVersion +
// SyncChecksum (and a wall-clock "merged-at" annotation), so GuaranteedUpdate's
// "same serialized contents" short-circuit never fired and the merged CP — plus
// a watch event to node-agent — was rewritten on every consolidation tick even
// when nothing changed (kubescape/storage#315 review). SaveMergedContainerProfile
// now carries the persisted merged object's identity forward (and reads the real
// current state rather than an empty cachedExistingObject), and the merge no
// longer stamps a per-tick timestamp, so an unchanged rebuild is recognised and
// the write is skipped — keeping the merged CP's ResourceVersion stable.
//
// A positive DeleteThreshold lets the consolidator revisit the (now idle/expired)
// workload on later ticks without new time-series data — that revisit is what
// re-runs the merge and would expose a spurious rewrite. The p1 fixture carries
// no report timestamp, so its consolidated TS row is always "expired".
func TestConsolidateUserManagedIdempotent(t *testing.T) {
	h := newE2EHarness(t)
	defer h.close()
	h.processor.DeleteThreshold = time.Second

	h.createCP("testdata/p1.json")
	userAP := &softwarecomposition.ApplicationProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: e2eNS, Name: e2eWorkloadUg},
		Spec: softwarecomposition.ApplicationProfileSpec{
			Containers: []softwarecomposition.ApplicationProfileContainer{
				{Name: "coredns", Capabilities: []string{"USER_MANAGED_CAP"}},
			},
		},
	}
	h.seedNonCP(e2eUgAPKey(), userAP)

	drainMergedWrites, stopWatch := h.watchMergedModifications()
	defer stopWatch()

	h.consolidate()
	first := h.requireMerged()
	require.Equal(t, 1, count(first.Spec.Capabilities, "USER_MANAGED_CAP"))
	require.NotEmpty(t, first.ResourceVersion)
	require.Equal(t, 1, drainMergedWrites(), "first tick must create the merged CP")

	// Cross a wall-clock second boundary before the no-data tick. The merge must
	// be a pure function of (observed, ug-AP, ug-NN) — independent of when it
	// runs — so a re-merge of identical inputs after time has advanced must still
	// be a no-op. This deterministically catches any reintroduced per-tick
	// timestamp (e.g. a "merged-at" annotation), which would otherwise only flake
	// the assertions below when a tick happened to straddle a second.
	time.Sleep(1100 * time.Millisecond)

	// Second tick: no new time-series data and the ug- AP unchanged. The
	// consolidator still revisits the workload (expired), re-running the merge
	// with identical inputs. The rebuilt merged must be recognised as unchanged
	// and NOT rewritten: no watch event fires, the persisted object is byte-for-
	// byte identical, and its ResourceVersion is stable.
	h.consolidate()
	second := h.requireMerged()
	assert.Equal(t, 0, drainMergedWrites(),
		"unchanged inputs must not rewrite the merged CP (a write ⇒ spurious watch event to node-agent)")
	assert.Equal(t, first.ResourceVersion, second.ResourceVersion,
		"unchanged inputs must keep the merged CP ResourceVersion stable")
	assert.Equal(t, first, second, "an unchanged tick must leave the merged CP byte-for-byte identical")
	assert.Equal(t, 1, count(second.Spec.Capabilities, "USER_MANAGED_CAP"),
		"unchanged ug- AP must not duplicate merged entries")

	// Third tick: edit the ug- AP. Now an input changed, so the merged CP must
	// be rewritten — its ResourceVersion advances and the new capability lands
	// (and the old one is retracted, since the merge is rebuilt from scratch).
	h.replaceUserAP(softwarecomposition.ApplicationProfileSpec{
		Containers: []softwarecomposition.ApplicationProfileContainer{
			{Name: "coredns", Capabilities: []string{"USER_MANAGED_CAP_V2"}},
		},
	})
	h.consolidate()
	third := h.requireMerged()
	assert.GreaterOrEqual(t, drainMergedWrites(), 1,
		"a changed ug- AP must rewrite the merged CP (a watch event must fire)")
	assert.NotEqual(t, second.ResourceVersion, third.ResourceVersion,
		"a changed ug- AP must rewrite the merged CP (RV must advance)")
	assert.Contains(t, third.Spec.Capabilities, "USER_MANAGED_CAP_V2",
		"merged CP must pick up the edited ug- AP capability")
	assert.NotContains(t, third.Spec.Capabilities, "USER_MANAGED_CAP",
		"merge is rebuilt from scratch, so the superseded capability must be retracted")
}

// TestConsolidateUserManagedRVBump verifies that updating the ug- AP (bumping
// its ResourceVersion) causes the next consolidation to apply the new content.
// New entries appear, and the RV marker advances.
func TestConsolidateUserManagedRVBump(t *testing.T) {
	h := newE2EHarness(t)
	defer h.close()

	h.createCP("testdata/p1.json")
	userAP := &softwarecomposition.ApplicationProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: e2eNS, Name: e2eWorkloadUg},
		Spec: softwarecomposition.ApplicationProfileSpec{
			Containers: []softwarecomposition.ApplicationProfileContainer{
				{Name: "coredns", Capabilities: []string{"V1_CAP"}},
			},
		},
	}
	h.seedNonCP(e2eUgAPKey(), userAP)
	h.consolidate()
	first := h.requireMerged()
	require.Contains(t, first.Spec.Capabilities, "V1_CAP")
	rvAfterFirst := first.Annotations[mergedSourceUserAPRVKey]

	// Bump ug- AP: replace with a new spec carrying a different capability.
	// Because the merged is rebuilt fresh from observed + ug- inputs, the new
	// V2_CAP appears and V1_CAP is retracted (the maintainer's primary
	// motivation for moving to a derived artifact).
	h.replaceUserAP(softwarecomposition.ApplicationProfileSpec{
		Containers: []softwarecomposition.ApplicationProfileContainer{
			{Name: "coredns", Capabilities: []string{"V2_CAP"}},
		},
	})

	h.createCP("testdata/p2.json")
	h.consolidate()
	second := h.requireMerged()

	assert.Contains(t, second.Spec.Capabilities, "V2_CAP",
		"new ug- entries must appear after RV bump")
	assert.NotContains(t, second.Spec.Capabilities, "V1_CAP",
		"retraction: V1_CAP must not survive a ug- AP replacement")
	assert.NotEqual(t, rvAfterFirst, second.Annotations[mergedSourceUserAPRVKey],
		"merged source-AP-RV annotation must advance after ug- update")
}

// TestConsolidateNoUserManaged verifies the merge path is a no-op (no error,
// no marker annotations) when no ug- AP/NN exists for the workload.
func TestConsolidateNoUserManaged(t *testing.T) {
	h := newE2EHarness(t)
	defer h.close()

	h.createCP("testdata/p1.json")
	h.consolidate()

	// Observed must exist and carry no merge metadata.
	observed := h.loadConsolidated()
	assert.NotContains(t, observed.Labels, MergedProfileLabelKey)
	assert.NotContains(t, observed.Annotations, mergedSourceUserAPKey)
	assert.NotContains(t, observed.Annotations, mergedSourceUserNNKey)

	// No merged artifact should have been written when no ug- input exists.
	_, ok := h.loadMerged()
	assert.False(t, ok, "merged artifact must not exist when no ug- input is present")
}

// TestConsolidateUserManagedPreservesStatus verifies the additive contract:
// status / completion annotations are derived from the time-series flow and
// must NOT be touched by the user-managed merge.
func TestConsolidateUserManagedPreservesStatus(t *testing.T) {
	h := newE2EHarness(t)
	defer h.close()

	h.createCP("testdata/p1.json")
	userAP := &softwarecomposition.ApplicationProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: e2eNS, Name: e2eWorkloadUg},
		Spec: softwarecomposition.ApplicationProfileSpec{
			Containers: []softwarecomposition.ApplicationProfileContainer{
				// Set values that, if naively copied, would clobber base CP
				// status/completion. The merge must ignore these and only
				// touch Spec slices.
				{Name: "coredns", Capabilities: []string{"X"}},
			},
		},
	}
	// Adding annotations that look like base-CP status/completion to the ug-
	// AP itself — these live on userAP.Annotations, never on its Spec, and
	// must not bleed into the consolidated CP's annotations.
	userAP.Annotations = map[string]string{
		helpersv1.StatusMetadataKey:     "should-not-overwrite",
		helpersv1.CompletionMetadataKey: "should-not-overwrite",
	}
	h.seedNonCP(e2eUgAPKey(), userAP)

	h.consolidate()
	cp := h.requireMerged()

	// Status/completion came from time-series flow — not from userAP.
	assert.NotEqual(t, "should-not-overwrite", cp.Annotations[helpersv1.StatusMetadataKey])
	assert.NotEqual(t, "should-not-overwrite", cp.Annotations[helpersv1.CompletionMetadataKey])
}

// TestMergeUserNNIntoCP_LabelSelectorUserOverridesBase verifies the override
// semantics introduced by overrideMerge: when both base and user supply a
// MatchLabels value for the same key, the user value wins. Distinct from
// utils.MergeMaps which preserves base on collision.
func TestMergeUserNNIntoCP_LabelSelectorUserOverridesBase(t *testing.T) {
	cp := &softwarecomposition.ContainerProfile{
		Spec: softwarecomposition.ContainerProfileSpec{
			LabelSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "x", "keep": "me"},
			},
		},
	}
	userNN := &softwarecomposition.NetworkNeighborhood{
		Spec: softwarecomposition.NetworkNeighborhoodSpec{
			LabelSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "y", "tier": "backend"},
			},
		},
	}
	mergeUserNNIntoCP(cp, userNN, "missing")
	assert.Equal(t, map[string]string{"app": "y", "keep": "me", "tier": "backend"}, cp.Spec.LabelSelector.MatchLabels)
}

// TestConsolidateUserManagedNNRVBump mirrors TestConsolidateUserManagedRVBump
// for NetworkNeighborhood: bumping the ug- NN's ResourceVersion must cause the
// next consolidation to re-merge.
func TestConsolidateUserManagedNNRVBump(t *testing.T) {
	h := newE2EHarness(t)
	defer h.close()

	h.createCP("testdata/p1.json")
	userNN := &softwarecomposition.NetworkNeighborhood{
		ObjectMeta: metav1.ObjectMeta{Namespace: e2eNS, Name: e2eWorkloadUg},
		Spec: softwarecomposition.NetworkNeighborhoodSpec{
			Containers: []softwarecomposition.NetworkNeighborhoodContainer{
				{
					Name: "coredns",
					Egress: []softwarecomposition.NetworkNeighbor{
						{Identifier: "v1-egress", DNSNames: []string{"v1.example"}},
					},
				},
			},
		},
	}
	h.seedNonCP(e2eUgNNKey(), userNN)

	h.consolidate()
	first := h.requireMerged()
	require.True(t, hasNeighbor(first.Spec.Egress, "v1-egress"), "first tick: v1-egress should be merged")
	rvAfterFirst := first.Annotations[mergedSourceUserNNRVKey]
	require.NotEmpty(t, rvAfterFirst)

	// Bump the ug- NN: replace egress with a new identifier.
	tryUpdate := func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
		out := input.DeepCopyObject().(*softwarecomposition.NetworkNeighborhood)
		out.Spec.Containers[0].Egress = []softwarecomposition.NetworkNeighbor{
			{Identifier: "v2-egress", DNSNames: []string{"v2.example"}},
		}
		return out, nil, nil
	}
	prev := h.s.processor
	h.s.processor = DefaultProcessor{}
	require.NoError(t, h.s.GuaranteedUpdateWithConn(
		h.ctx, h.conn, e2eUgNNKey(), &softwarecomposition.NetworkNeighborhood{},
		false, nil, tryUpdate, nil, ""))
	h.s.processor = prev

	h.createCP("testdata/p2.json")
	h.consolidate()
	second := h.requireMerged()

	assert.True(t, hasNeighbor(second.Spec.Egress, "v2-egress"),
		"second tick: v2-egress must appear after RV bump")
	assert.False(t, hasNeighbor(second.Spec.Egress, "v1-egress"),
		"retraction: v1-egress must not survive a ug- NN replacement")
	assert.NotEqual(t, rvAfterFirst, second.Annotations[mergedSourceUserNNRVKey],
		"merged source-NN-RV annotation must advance after ug- NN update")
}

func hasNeighbor(neighbors []softwarecomposition.NetworkNeighbor, identifier string) bool {
	for _, n := range neighbors {
		if n.Identifier == identifier {
			return true
		}
	}
	return false
}

// TestConsolidateUserManagedFanOut exercises slug fan-out: a single
// ug-<workloadSlug> AP listing two containers must be merged into BOTH
// per-container CPs the consolidation flow produces. Uses the fixture
// workload "multiple-containers-deployment-d4b8dd5fd" which has separate
// per-container TS profiles for "server" and "nginx".
func TestConsolidateUserManagedFanOut(t *testing.T) {
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	defer func(p *sqlitemigration.Pool) { _ = p.Close() }(pool)
	conn, err := pool.Take(context.TODO())
	require.NoError(t, err)
	defer pool.Put(conn)

	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	processor := &ContainerProfileProcessor{
		DeleteThreshold:         0,
		MaxContainerProfileSize: 40000,
		HostType:                armotypes.HostTypeKubernetes,
	}
	s := &StorageImpl{
		appFs:           afero.NewMemMapFs(),
		pool:            pool,
		locks:           utils.NewMapMutex[string](),
		processor:       processor,
		root:            DefaultStorageRoot,
		scheme:          sch,
		versioner:       storage.APIObjectVersioner{},
		watchDispatcher: NewWatchDispatcher(),
	}
	processor.SetStorage(NewContainerProfileStorageImpl(s, pool))

	ctx, cancel := context.WithTimeout(context.TODO(), 15*time.Second)
	defer cancel()

	createCP := func(f string) {
		content, err := os.ReadFile(f)
		require.NoError(t, err)
		var profile softwarecomposition.ContainerProfile
		require.NoError(t, json.Unmarshal(content, &profile))
		require.NoError(t, s.Create(ctx,
			"/spdx.softwarecomposition.kubescape.io/containerprofile/"+profile.Namespace+"/"+profile.Name,
			&profile, nil, 0))
	}
	// p10 (server) and p12 (nginx) belong to the same workload
	// (replicaset-multiple-containers-deployment-d4b8dd5fd) in namespace
	// node-agent-test-hjjz. Their consolidated CPs share the workload slug.
	createCP("testdata/p10.json")
	createCP("testdata/p12.json")

	const ns = "node-agent-test-hjjz"
	const ugName = "ug-replicaset-multiple-containers-deployment-d4b8dd5fd"
	ugKey := BuildContainerProfileKey(armotypes.ProfileIdentifier{
		ProfileScope: armotypes.ProfileScope{HostType: armotypes.HostTypeKubernetes, Namespace: ns},
		Name:         ugName,
	}, "applicationprofiles")

	userAP := &softwarecomposition.ApplicationProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: ugName},
		Spec: softwarecomposition.ApplicationProfileSpec{
			Containers: []softwarecomposition.ApplicationProfileContainer{
				{Name: "server", Capabilities: []string{"FANOUT_SERVER"}},
				{Name: "nginx", Capabilities: []string{"FANOUT_NGINX"}},
			},
		},
	}
	prev := s.processor
	s.processor = DefaultProcessor{}
	require.NoError(t, s.Create(ctx, ugKey, userAP, nil, 0))
	s.processor = prev

	require.NoError(t, processor.ConsolidateTimeSeries(ctx))

	// Both per-container CPs must carry the matching merge in the merged
	// artifact (not on the observed CP — that one stays pure time-series).
	loadCP := func(name string) softwarecomposition.ContainerProfile {
		var cp softwarecomposition.ContainerProfile
		observedKey := BuildContainerProfileKey(armotypes.ProfileIdentifier{
			ProfileScope: armotypes.ProfileScope{HostType: armotypes.HostTypeKubernetes, Namespace: ns},
			Name:         name,
		}, "containerprofile")
		require.NoError(t, s.GetWithConn(ctx, conn, MergedKeyFor(observedKey), storage.GetOptions{}, &cp))
		return cp
	}

	serverCP := loadCP("replicaset-multiple-containers-deployment-d4b8dd5fd-server-5cad-76b6")
	nginxCP := loadCP("replicaset-multiple-containers-deployment-d4b8dd5fd-nginx-42c9-63c3")

	assert.Contains(t, serverCP.Spec.Capabilities, "FANOUT_SERVER", "server CP missed user-managed merge")
	assert.NotContains(t, serverCP.Spec.Capabilities, "FANOUT_NGINX", "server CP should not receive nginx's user-managed entries")
	assert.Contains(t, nginxCP.Spec.Capabilities, "FANOUT_NGINX", "nginx CP missed user-managed merge")
	assert.NotContains(t, nginxCP.Spec.Capabilities, "FANOUT_SERVER", "nginx CP should not receive server's user-managed entries")
}

// TestConsolidateRetractsMergedOnUgAPDelete is the central correctness test
// for the maintainer's stale-on-delete concern. After a successful merge,
// removing the ug- AP must cause the merged artifact to disappear so node-
// agent falls back to the observed CP — i.e., the user's permission grant is
// retracted.
func TestConsolidateRetractsMergedOnUgAPDelete(t *testing.T) {
	h := newE2EHarness(t)
	defer h.close()

	h.createCP("testdata/p1.json")
	userAP := &softwarecomposition.ApplicationProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: e2eNS, Name: e2eWorkloadUg},
		Spec: softwarecomposition.ApplicationProfileSpec{
			Containers: []softwarecomposition.ApplicationProfileContainer{
				{Name: "coredns", Capabilities: []string{"DOOMED_CAP"}},
			},
		},
	}
	h.seedNonCP(e2eUgAPKey(), userAP)

	h.consolidate()
	require.Contains(t, h.requireMerged().Spec.Capabilities, "DOOMED_CAP",
		"first tick: merged should reflect ug- AP")

	// Delete the ug- AP outside the consolidation path.
	prev := h.s.processor
	h.s.processor = DefaultProcessor{}
	require.NoError(t, h.s.Delete(h.ctx, e2eUgAPKey(), &softwarecomposition.ApplicationProfile{},
		nil, storage.ValidateAllObjectFunc, nil, storage.DeleteOptions{}))
	h.s.processor = prev

	// Re-consolidate with new TS data so the workload is visited again.
	h.createCP("testdata/p2.json")
	h.consolidate()

	_, ok := h.loadMerged()
	assert.False(t, ok, "merged artifact must be deleted after ug- AP is removed")

	// Observed must still exist and never have contained DOOMED_CAP.
	observed := h.loadConsolidated()
	assert.NotContains(t, observed.Spec.Capabilities, "DOOMED_CAP",
		"observed CP must never have been mutated by the ug- merge")
}

// TestConsolidateRefreshesMergedOnNoNewData verifies that updateProfile's
// merged refresh runs even when the time-series merge produced no new data
// this tick. The earlier !newData early-return short-circuited this path and
// stranded the merged artifact (kubescape/storage#315 review step 5).
//
// Scope note: a truly idle workload with zero hasData=1 TS rows isn't visited
// by ConsolidateTimeSeries at all — that case requires a separate trigger
// (option a, watch-driven enqueue) and is out of scope here per matthyx's
// "option (c)" decision. This test covers the in-tick !newData case where
// the consolidator still visits the workload.
func TestConsolidateRefreshesMergedOnNoNewData(t *testing.T) {
	h := newE2EHarness(t)
	defer h.close()

	h.createCP("testdata/p1.json")
	// No ug- input on the first tick: consolidate to drain TS data into
	// observed; merged should not exist.
	h.consolidate()
	_, ok := h.loadMerged()
	require.False(t, ok, "preconditions: no merged before ug- is added")

	// Add ug- AP, then re-run consolidation. The same workload may still be
	// visited because there's a (possibly stale) TS row queued by createCP;
	// even if processTimeSeries returns no new data, the merged refresh must
	// still execute.
	userAP := &softwarecomposition.ApplicationProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: e2eNS, Name: e2eWorkloadUg},
		Spec: softwarecomposition.ApplicationProfileSpec{
			Containers: []softwarecomposition.ApplicationProfileContainer{
				{Name: "coredns", Capabilities: []string{"LATE_ADDITION"}},
			},
		},
	}
	h.seedNonCP(e2eUgAPKey(), userAP)

	// Inject a fresh TS row so the consolidator visits the workload. We're
	// proving that the merged refresh fires even when the merge itself
	// doesn't generate new spec entries (the data is unchanged from the prior
	// tick) — what matters is that ug- propagation isn't gated on TS newness.
	h.createCP("testdata/p1.json")
	h.consolidate()

	merged := h.requireMerged()
	assert.Contains(t, merged.Spec.Capabilities, "LATE_ADDITION",
		"merged refresh must propagate a late-added ug- AP")
}

// TestRESTWrapper_MergedFirstFallback exercises the consumer-side read path:
// the REST wrapper prefers the merged artifact, falls back to observed when
// no merged exists, and surfaces NotFound when both are absent.
func TestRESTWrapper_MergedFirstFallback(t *testing.T) {
	h := newE2EHarness(t)
	defer h.close()

	rest := NewContainerProfileRESTStorage(h.s)
	observedKey := e2eCPKey()
	mergedKey := e2eMergedCPKey()

	// Stage 1: neither observed nor merged exists.
	var got softwarecomposition.ContainerProfile
	err := rest.Get(h.ctx, observedKey, storage.GetOptions{}, &got)
	require.Error(t, err, "expected NotFound when neither observed nor merged exists")
	assert.True(t, storage.IsNotFound(err))

	// Stage 2: only observed exists. Wrapper falls back.
	observed := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: e2eNS, Name: e2eContainerCPName,
			Labels: map[string]string{"source": "observed"},
		},
	}
	prev := h.s.processor
	h.s.processor = DefaultProcessor{}
	require.NoError(t, h.s.Create(h.ctx, observedKey, observed, nil, 0))

	got = softwarecomposition.ContainerProfile{}
	require.NoError(t, rest.Get(h.ctx, observedKey, storage.GetOptions{}, &got))
	assert.Equal(t, "observed", got.Labels["source"], "fallback must return observed when no merged exists")

	// Stage 3: merged exists; wrapper prefers it.
	merged := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: e2eNS, Name: e2eContainerCPName,
			Labels: map[string]string{"source": "merged", MergedProfileLabelKey: MergedProfileLabelValue},
		},
	}
	require.NoError(t, h.s.Create(h.ctx, mergedKey, merged, nil, 0))
	h.s.processor = prev

	got = softwarecomposition.ContainerProfile{}
	require.NoError(t, rest.Get(h.ctx, observedKey, storage.GetOptions{}, &got))
	assert.Equal(t, "merged", got.Labels["source"], "wrapper must prefer merged when present")
}

// failingDeleteStore wraps a StorageQuerier and injects an error for Delete on
// one specific key, passing every other call straight through. It exists to
// prove the REST wrapper surfaces merged-sibling delete failures rather than
// swallowing them.
type failingDeleteStore struct {
	StorageQuerier
	failKey string
	err     error
}

func (f failingDeleteStore) Delete(ctx context.Context, key string, out runtime.Object, preconditions *storage.Preconditions, validateDeletion storage.ValidateObjectFunc, cachedExistingObject runtime.Object, opts storage.DeleteOptions) error {
	if key == f.failKey {
		return f.err
	}
	return f.StorageQuerier.Delete(ctx, key, out, preconditions, validateDeletion, cachedExistingObject, opts)
}

// TestRESTWrapper_MergedDeleteFailurePropagates asserts that a non-NotFound
// failure deleting the merged sibling is returned as a hard error (so the
// apiserver retries) instead of being swallowed, which would orphan the merged
// artifact and let the merged-first read path keep serving a profile whose
// observed sibling is gone.
func TestRESTWrapper_MergedDeleteFailurePropagates(t *testing.T) {
	h := newE2EHarness(t)
	defer h.close()

	errBoom := errors.New("storage unavailable")
	rest := NewContainerProfileRESTStorage(failingDeleteStore{
		StorageQuerier: h.s,
		failKey:        e2eMergedCPKey(),
		err:            errBoom,
	})

	var out softwarecomposition.ContainerProfile
	err := rest.Delete(h.ctx, e2eCPKey(), &out, nil, storage.ValidateAllObjectFunc, nil, storage.DeleteOptions{})
	require.Error(t, err, "merged-sibling delete failure must propagate")
	assert.ErrorIs(t, err, errBoom)
	assert.ErrorContains(t, err, "merged container profile sibling")
}

// fakeRunningFetcher reports a fixed namespace list and running-workload set,
// standing in for the live Kubernetes discovery the production cleanup uses.
type fakeRunningFetcher struct {
	namespaces []string
	running    *maps.SafeMap[string, mapset.Set[string]]
}

func (f fakeRunningFetcher) ListNamespaces(_ *sqlite.Conn) ([]string, error) {
	return f.namespaces, nil
}

func (f fakeRunningFetcher) FetchResources(_ string) (ResourceMaps, error) {
	return ResourceMaps{
		RunningContainerImageIds:     mapset.NewSet[string](),
		RunningInstanceIds:           mapset.NewSet[string](),
		RunningTemplateHash:          mapset.NewSet[string](),
		RunningWlidsToContainerNames: f.running,
	}, nil
}

// TestCleanupRetiresMergedOrphan proves the merged-CP kind is wired into the
// cleanup map (matthyx review, ask #1): a merged CP whose workload is no longer
// running is age-cleaned, while a merged CP for a running workload survives.
// This covers the path where a workload is retired without going through the
// REST Delete cascade that maintains the merged sibling.
func TestCleanupRetiresMergedOrphan(t *testing.T) {
	h := newE2EHarness(t)
	defer h.close()

	const ns = "cleanup-ns"
	mergedKey := func(name string) string {
		return MergedKeyFor(BuildContainerProfileKey(armotypes.ProfileIdentifier{
			ProfileScope: armotypes.ProfileScope{HostType: armotypes.HostTypeKubernetes, Namespace: ns},
			Name:         name,
		}, "containerprofile"))
	}
	goneWlid := "wlid://cluster-test/namespace-" + ns + "/deployment-gone"
	runningWlid := "wlid://cluster-test/namespace-" + ns + "/deployment-running"

	// Seed two merged CPs directly through the storage layer (processor swapped
	// out so AfterCreate doesn't intercept) so both the payload file and the
	// SQLite metadata land where the cleanup walk + readMetadata expect them.
	prev := h.s.processor
	h.s.processor = DefaultProcessor{}
	writeMerged := func(name, wlid string) {
		cp := &softwarecomposition.ContainerProfile{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   ns,
				Name:        name,
				Annotations: map[string]string{helpersv1.WlidMetadataKey: wlid},
				Labels:      map[string]string{MergedProfileLabelKey: MergedProfileLabelValue},
			},
		}
		require.NoError(t, h.s.Create(h.ctx, mergedKey(name), cp, nil, 0))
	}
	writeMerged("gone", goneWlid)
	writeMerged("running", runningWlid)
	h.s.processor = prev

	// Discovery reports only the running workload as alive.
	running := new(maps.SafeMap[string, mapset.Set[string]])
	running.Set(wlidWithoutClusterName(runningWlid), mapset.NewSet[string]())

	var deleted []string
	handler := &ResourcesCleanupHandler{
		appFs: h.s.appFs,
		pool:  h.pool,
		root:  DefaultStorageRoot,
		// Distinct from ns so the final defaultNamespace pass (which carries an
		// empty running-wlid set) walks an unrelated, empty directory rather than
		// recursing back over our test namespace and sweeping the survivor.
		defaultNamespace: "kubescape",
		fetcher: fakeRunningFetcher{
			namespaces: []string{ns},
			running:    running,
		},
		deleteFunc: func(appFs afero.Fs, path string) {
			require.NoError(t, appFs.Remove(path))
			deleted = append(deleted, path)
		},
	}

	require.NoError(t, handler.CleanupTask(h.ctx, map[string][]TypeCleanupHandlerFunc{
		ContainerProfileMergedKind: {deleteByTemplateHashOrWlid},
	}))

	require.Len(t, deleted, 1, "exactly the orphan merged CP should be deleted")
	assert.Contains(t, deleted[0], ContainerProfileMergedKind, "deleted file must be a merged CP")
	assert.Contains(t, deleted[0], "/gone", "the orphan, not the running workload, must be deleted")
}

// TestE2EScenario_Walkthrough is a verbose end-to-end scenario that prints
// observed-vs-merged state at every step. Run with `go test -run
// TestE2EScenario_Walkthrough -v` to watch the new design behave: ug- adds,
// retractions on edit and delete, and the REST wrapper's merged-first read.
// Not an assertion-heavy test — its job is to make the behavior legible.
func TestE2EScenario_Walkthrough(t *testing.T) {
	h := newE2EHarness(t)
	defer h.close()

	rest := NewContainerProfileRESTStorage(h.s)
	getViaREST := func() (softwarecomposition.ContainerProfile, error) {
		var cp softwarecomposition.ContainerProfile
		err := rest.Get(h.ctx, e2eCPKey(), storage.GetOptions{}, &cp)
		return cp, err
	}

	dumpState := func(label string) {
		t.Logf("=== %s ===", label)

		var observed softwarecomposition.ContainerProfile
		obsErr := h.s.GetWithConn(h.ctx, h.conn, e2eCPKey(), storage.GetOptions{}, &observed)
		if obsErr != nil {
			t.Logf("  observed: <%s>", obsErr.Error())
		} else {
			t.Logf("  observed capabilities: %v", observed.Spec.Capabilities)
			t.Logf("  observed has merge-label: %v", observed.Labels[MergedProfileLabelKey])
		}

		merged, mergedOK := h.loadMerged()
		if !mergedOK {
			t.Logf("  merged:   <absent>")
		} else {
			t.Logf("  merged capabilities:   %v", merged.Spec.Capabilities)
			t.Logf("  merged label:          %s=%s", MergedProfileLabelKey, merged.Labels[MergedProfileLabelKey])
			t.Logf("  merged source ug-ap:   %s (rv=%s)", merged.Annotations[mergedSourceUserAPKey], merged.Annotations[mergedSourceUserAPRVKey])
			t.Logf("  merged source ug-nn:   %s (rv=%s)", merged.Annotations[mergedSourceUserNNKey], merged.Annotations[mergedSourceUserNNRVKey])
		}

		viaREST, restErr := getViaREST()
		if restErr != nil {
			t.Logf("  REST GET: <%s>", restErr.Error())
		} else {
			t.Logf("  REST GET capabilities: %v (label-kind=%q → %s)",
				viaREST.Spec.Capabilities, viaREST.Labels[MergedProfileLabelKey],
				map[bool]string{true: "served merged", false: "served observed"}[viaREST.Labels[MergedProfileLabelKey] == MergedProfileLabelValue])
		}
		t.Logf("")
	}

	t.Log("Scenario: simulate node-agent reads across the ug- AP lifecycle")
	t.Log("Workload slug:", e2eWorkloadSlug, " container CP name:", e2eContainerCPName)
	t.Log("")

	// Step 1: time-series data arrives from node-agent, no ug- yet.
	h.createCP("testdata/p1.json")
	h.consolidate()
	dumpState("Step 1: TS data only — no ug-")

	// Step 2: operator creates a ug- AP granting an extra capability.
	userAP := &softwarecomposition.ApplicationProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: e2eNS, Name: e2eWorkloadUg},
		Spec: softwarecomposition.ApplicationProfileSpec{
			Containers: []softwarecomposition.ApplicationProfileContainer{
				{Name: "coredns", Capabilities: []string{"NET_ADMIN_FROM_UG"}},
			},
		},
	}
	h.seedNonCP(e2eUgAPKey(), userAP)
	h.createCP("testdata/p2.json") // fresh TS row so consolidator visits
	h.consolidate()
	dumpState("Step 2: operator adds ug- AP granting NET_ADMIN_FROM_UG")

	// Step 3: operator edits the ug- AP — replaces the capability list. The
	// previous in-place merge couldn't retract; the new design must.
	h.replaceUserAP(softwarecomposition.ApplicationProfileSpec{
		Containers: []softwarecomposition.ApplicationProfileContainer{
			{Name: "coredns", Capabilities: []string{"SYS_PTRACE_FROM_UG"}},
		},
	})
	h.createCP("testdata/p1.json")
	h.consolidate()
	dumpState("Step 3: operator edits ug- AP (NET_ADMIN_FROM_UG → SYS_PTRACE_FROM_UG)")

	// Step 4: operator deletes the ug- AP. The merged artifact must disappear
	// and the REST wrapper must transparently fall back to observed.
	prev := h.s.processor
	h.s.processor = DefaultProcessor{}
	require.NoError(t, h.s.Delete(h.ctx, e2eUgAPKey(), &softwarecomposition.ApplicationProfile{},
		nil, storage.ValidateAllObjectFunc, nil, storage.DeleteOptions{}))
	h.s.processor = prev
	h.createCP("testdata/p2.json")
	h.consolidate()
	dumpState("Step 4: operator deletes ug- AP — retraction")
}

// TestConsolidatorReadsObservedOnly proves the consolidator's read path never
// pulls from the merged key. We seed a poisoned merged artifact with content
// that, if mistakenly used as the consolidation base, would surface in the
// next observed CP. After a tick, the observed CP must be free of the poison.
func TestConsolidatorReadsObservedOnly(t *testing.T) {
	h := newE2EHarness(t)
	defer h.close()

	h.createCP("testdata/p1.json")
	h.consolidate() // populate observed

	// Poison the merged key with a capability that doesn't exist anywhere else.
	prev := h.s.processor
	h.s.processor = DefaultProcessor{}
	poison := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: e2eNS, Name: e2eContainerCPName},
		Spec:       softwarecomposition.ContainerProfileSpec{Capabilities: []string{"POISON_FROM_MERGED"}},
	}
	require.NoError(t, h.s.Create(h.ctx, e2eMergedCPKey(), poison, nil, 0))
	h.s.processor = prev

	// Run another consolidation tick with fresh TS data; the consolidator's
	// loadOrInitializeProfile must read observed, not merged.
	h.createCP("testdata/p2.json")
	h.consolidate()

	observed := h.loadConsolidated()
	assert.NotContains(t, observed.Spec.Capabilities, "POISON_FROM_MERGED",
		"consolidator must read observed, never merged — poisoned merged content leaked into observed")
}
