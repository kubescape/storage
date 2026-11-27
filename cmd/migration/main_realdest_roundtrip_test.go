package main

// Round-trip test against the REAL destination type.
//
// The golden test in main_test.go encodes fixtures with the same Legacy*
// structs the binary decodes into and asserts the JSON output against those
// same structs. That proves gob->JSON is lossless *within the tool's own
// layout*, but it cannot detect a mismatch between the tool's JSON output and
// what the storage Get path actually unmarshals it into: the internal
// softwarecomposition.ContainerProfile (storage.go rewrites the object from
// exactly that unmarshal).
//
// This test closes that gap:
//
//	(a) the fixture is gob-encoded from an INDEPENDENT historical struct
//	    definition (histContainerProfile below), not the binary's Legacy*
//	    types, mirroring the layout the old storage binary persisted;
//	(b) the real built binary migrates it;
//	(c) the JSON output is unmarshalled into the actual destination type,
//	    softwarecomposition.ContainerProfile, and every field is asserted to
//	    survive - in particular Spec.MatchLabels / Spec.MatchExpressions,
//	    whose loss would downstream turn a generated NetworkPolicy's
//	    podSelector into {} (select every pod in the namespace).

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

// Historical struct definitions, written independently from the binary's
// Legacy* types. Field NAMES are what gob matches on, so these reproduce the
// field layout of the internal types at the time the legacy payloads were
// written (uint64 seccomp numerics, embedded LabelSelector in the spec).
// Embedded structs are encoded by gob as a field named after the embedded
// type, so SpecBase/LabelSelector below are explicit named fields carrying
// exactly those gob field names.

type histArg struct {
	Index    uint64
	Value    uint64
	ValueTwo uint64
	Op       string
}

type histSyscall struct {
	Names    []string
	Action   string
	ErrnoRet uint64
	Args     []*histArg
}

type histSpecBase struct {
	Disabled bool
}

type histSeccompInner struct {
	SpecBase         histSpecBase // gob field name "SpecBase" (embedded in the historical type)
	BaseProfileName  string
	DefaultAction    string
	Architectures    []string
	ListenerPath     string
	ListenerMetadata string
	Syscalls         []*histSyscall
	Flags            []string
}

type histSingleSeccompProfile struct {
	Name string
	Path string
	Spec histSeccompInner
}

type histExecCalls struct {
	Path string
	Args []string
	Envs []string
}

type histOpenCalls struct {
	Path  string
	Flags []string
}

type histNetworkPort struct {
	Name     string
	Protocol string
	Port     *int32
}

type histNetworkNeighbor struct {
	Identifier        string
	Type              string
	DNS               string
	DNSNames          []string
	Ports             []histNetworkPort
	PodSelector       *metav1.LabelSelector
	NamespaceSelector *metav1.LabelSelector
	IPAddress         string
}

type histHTTPEndpoint struct {
	Endpoint  string
	Methods   []string
	Internal  bool
	Direction string
	Headers   []byte
}

type histRulePolicy struct {
	AllowedProcesses []string
	AllowedContainer bool
}

type histContainerProfileSpec struct {
	Architectures  []string
	Capabilities   []string
	Execs          []histExecCalls
	Opens          []histOpenCalls
	Syscalls       []string
	SeccompProfile histSingleSeccompProfile
	Endpoints      []histHTTPEndpoint
	ImageID        string
	ImageTag       string
	PolicyByRuleId map[string]histRulePolicy
	// The historical internal ContainerProfileSpec EMBEDDED metav1.LabelSelector;
	// gob encodes that embed as a field named "LabelSelector".
	LabelSelector metav1.LabelSelector
	Ingress       []histNetworkNeighbor
	Egress        []histNetworkNeighbor
}

type histContainerProfile struct {
	TypeMeta   metav1.TypeMeta   // gob field name "TypeMeta" (embedded historically)
	ObjectMeta metav1.ObjectMeta // gob field name "ObjectMeta" (embedded historically)
	Spec       histContainerProfileSpec
	Status     struct{}
}

func histFixture() *histContainerProfile {
	port := int32(6379)
	h := &histContainerProfile{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ContainerProfile",
			APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "replicaset-redis-def456-redis-9f8e-7d6c",
			Namespace: "redis",
			Labels: map[string]string{
				"kubescape.io/workload-kind": "StatefulSet",
				"kubescape.io/workload-name": "redis",
			},
			Annotations: map[string]string{
				"kubescape.io/status":     "completed",
				"kubescape.io/completion": "complete",
			},
		},
	}
	h.Spec.Architectures = []string{"amd64"}
	h.Spec.Capabilities = []string{"NET_BIND_SERVICE"}
	h.Spec.Execs = []histExecCalls{{Path: "/usr/local/bin/redis-server", Args: []string{"/etc/redis.conf"}, Envs: []string{"HOME=/data"}}}
	h.Spec.Opens = []histOpenCalls{{Path: "/etc/redis.conf", Flags: []string{"O_RDONLY", "O_CLOEXEC"}}}
	h.Spec.Syscalls = []string{"epoll_wait", "read", "write"}
	h.Spec.ImageID = "sha256:cafebabe"
	h.Spec.ImageTag = "redis:7"
	h.Spec.Endpoints = []histHTTPEndpoint{{Endpoint: ":8080/healthz", Methods: []string{"GET"}, Internal: true, Direction: "inbound"}}
	h.Spec.PolicyByRuleId = map[string]histRulePolicy{
		"R0001": {AllowedProcesses: []string{"redis-server"}, AllowedContainer: true},
	}

	// The money fields: the workload's spec.selector labels, historically
	// stored via the spec's embedded LabelSelector.
	h.Spec.LabelSelector = metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app.kubernetes.io/name":     "redis",
			"app.kubernetes.io/instance": "redis",
		},
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{Key: "tier", Operator: metav1.LabelSelectorOpIn, Values: []string{"backend"}},
		},
	}

	h.Spec.SeccompProfile = histSingleSeccompProfile{Name: "redis", Path: "/redis"}
	h.Spec.SeccompProfile.Spec.DefaultAction = "SCMP_ACT_ERRNO"
	h.Spec.SeccompProfile.Spec.Syscalls = []*histSyscall{
		{
			Names:    []string{"ptrace"},
			Action:   "SCMP_ACT_ERRNO",
			ErrnoRet: 1,
			Args:     []*histArg{{Index: 0, Value: 42, ValueTwo: 7, Op: "SCMP_CMP_EQ"}},
		},
	}

	h.Spec.Ingress = []histNetworkNeighbor{
		{
			Identifier: "in-redis-client",
			Type:       "internal",
			Ports:      []histNetworkPort{{Name: "TCP-6379", Protocol: "TCP", Port: &port}},
			PodSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "redis-client"},
			},
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"kubernetes.io/metadata.name": "redis"},
			},
		},
	}
	h.Spec.Egress = []histNetworkNeighbor{
		{
			Identifier: "eg-dns",
			Type:       "internal",
			DNSNames:   []string{"kube-dns.kube-system.svc.cluster.local."},
			Ports:      []histNetworkPort{{Name: "UDP-53", Protocol: "UDP", Port: func() *int32 { p := int32(53); return &p }()}},
		},
	}
	return h
}

// TestMigration_RealDestinationRoundTrip pins the full pipeline the storage Get
// path performs on a legacy payload: historical gob -> migration binary ->
// json.Unmarshal into softwarecomposition.ContainerProfile -> (storage would
// rewrite that object over the original). Every asserted field is one the
// rewrite would otherwise silently drop.
func TestMigration_RealDestinationRoundTrip(t *testing.T) {
	bin := buildMigrationBinary(t)
	fixture := histFixture()
	path := writeGobFixture(t, fixture)

	stdout, stderr, code := runMigration(t, bin, "-file", path, "-type", "ContainerProfile")
	require.Equal(t, 0, code, "decode must succeed; stderr=%s", stderr)
	require.NotEmpty(t, stdout)

	var got softwarecomposition.ContainerProfile
	require.NoError(t, json.Unmarshal([]byte(stdout), &got),
		"output must unmarshal into the real destination type: %s", stdout)

	// Object identity.
	assert.Equal(t, fixture.ObjectMeta.Name, got.Name)
	assert.Equal(t, fixture.ObjectMeta.Namespace, got.Namespace)
	assert.Equal(t, fixture.ObjectMeta.Labels, got.Labels)
	assert.Equal(t, fixture.ObjectMeta.Annotations, got.Annotations)

	// Profile surfaces.
	assert.Equal(t, fixture.Spec.Architectures, got.Spec.Architectures)
	assert.Equal(t, fixture.Spec.Capabilities, got.Spec.Capabilities)
	require.Len(t, got.Spec.Execs, 1)
	assert.Equal(t, fixture.Spec.Execs[0].Path, got.Spec.Execs[0].Path)
	assert.Equal(t, fixture.Spec.Execs[0].Args, got.Spec.Execs[0].Args)
	require.Len(t, got.Spec.Opens, 1)
	assert.Equal(t, fixture.Spec.Opens[0].Path, got.Spec.Opens[0].Path)
	assert.Equal(t, fixture.Spec.Opens[0].Flags, got.Spec.Opens[0].Flags)
	assert.Equal(t, fixture.Spec.Syscalls, got.Spec.Syscalls)
	assert.Equal(t, fixture.Spec.ImageID, got.Spec.ImageID)
	assert.Equal(t, fixture.Spec.ImageTag, got.Spec.ImageTag)
	require.Len(t, got.Spec.Endpoints, 1)
	assert.Equal(t, fixture.Spec.Endpoints[0].Endpoint, got.Spec.Endpoints[0].Endpoint)
	require.Contains(t, got.Spec.PolicyByRuleId, "R0001")
	assert.Equal(t, fixture.Spec.PolicyByRuleId["R0001"].AllowedProcesses, got.Spec.PolicyByRuleId["R0001"].AllowedProcesses)

	// Seccomp uint64 -> int64 numerics.
	require.Len(t, got.Spec.SeccompProfile.Spec.Syscalls, 1)
	require.Len(t, got.Spec.SeccompProfile.Spec.Syscalls[0].Args, 1)
	assert.Equal(t, int64(42), got.Spec.SeccompProfile.Spec.Syscalls[0].Args[0].Value)

	// THE MONEY ASSERTIONS: the workload selector must survive into the
	// destination's embedded (promoted) LabelSelector. Losing these turns a
	// per-workload generated NetworkPolicy podSelector into {}.
	assert.Equal(t, fixture.Spec.LabelSelector.MatchLabels, got.Spec.MatchLabels,
		"Spec.MatchLabels must survive migration (embedded LabelSelector promotion)")
	assert.Equal(t, fixture.Spec.LabelSelector.MatchExpressions, got.Spec.MatchExpressions,
		"Spec.MatchExpressions must survive migration (embedded LabelSelector promotion)")

	// Network neighbors, including their own (named, non-embedded) selectors.
	require.Len(t, got.Spec.Ingress, 1)
	assert.Equal(t, fixture.Spec.Ingress[0].Identifier, got.Spec.Ingress[0].Identifier)
	require.NotNil(t, got.Spec.Ingress[0].PodSelector)
	assert.Equal(t, fixture.Spec.Ingress[0].PodSelector.MatchLabels, got.Spec.Ingress[0].PodSelector.MatchLabels)
	require.NotNil(t, got.Spec.Ingress[0].NamespaceSelector)
	assert.Equal(t, fixture.Spec.Ingress[0].NamespaceSelector.MatchLabels, got.Spec.Ingress[0].NamespaceSelector.MatchLabels)
	require.Len(t, got.Spec.Ingress[0].Ports, 1)
	require.NotNil(t, got.Spec.Ingress[0].Ports[0].Port)
	assert.Equal(t, int32(6379), *got.Spec.Ingress[0].Ports[0].Port)
	require.Len(t, got.Spec.Egress, 1)
	assert.Equal(t, fixture.Spec.Egress[0].DNSNames, got.Spec.Egress[0].DNSNames)
}
