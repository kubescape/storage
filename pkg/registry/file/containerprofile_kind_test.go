package file

import (
	"testing"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestIsContainerProfileKind pins the singular/plural acceptance and rejection of
// unrelated kinds. The storage keys use the SINGULAR kind; the plural is only the
// REST resource name, so both must be recognized while unrelated kinds are not.
func TestIsContainerProfileKind(t *testing.T) {
	tests := []struct {
		kind string
		want bool
	}{
		{ContainerProfileKind, true},       // "containerprofile"
		{ContainerProfileKindPlural, true}, // "containerprofiles"
		{"sbomsyft", false},                // unrelated
		{"networkneighborhood", false},     // unrelated
		{"", false},                        // empty
		{"ContainerProfile", false},        // case-sensitive: capitalized is not a stored kind
	}
	for _, tt := range tests {
		assert.Equalf(t, tt.want, IsContainerProfileKind(tt.kind), "IsContainerProfileKind(%q)", tt.kind)
	}
}

// TestNormalizeContainerProfileKind pins that the plural is folded to the
// singular stored kind while singular and unrelated kinds pass through unchanged.
func TestNormalizeContainerProfileKind(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{ContainerProfileKindPlural, ContainerProfileKind}, // plural -> singular
		{ContainerProfileKind, ContainerProfileKind},       // singular unchanged
		{"sbomsyft", "sbomsyft"},                           // unrelated unchanged
		{"", ""},                                           // empty unchanged
	}
	for _, tt := range tests {
		assert.Equalf(t, tt.want, NormalizeContainerProfileKind(tt.in), "NormalizeContainerProfileKind(%q)", tt.in)
	}
}

// TestWorkloadPolicyName pins the workload-level policy name derivation:
// <lower(kind)>-<workload-name>, falling back to the object name when the
// workload-name label is absent, and signalling skip when the kind label is
// missing entirely.
func TestWorkloadPolicyName(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		objName  string
		wantName string
		wantOK   bool
	}{
		{
			name: "kind + workload-name labels",
			labels: map[string]string{
				helpersv1.RelatedKindMetadataKey: "Deployment",
				helpersv1.RelatedNameMetadataKey: "nginx",
			},
			objName:  "replicaset-nginx-abc-nginx-1-2",
			wantName: "deployment-nginx", // lower(kind)-workloadname
			wantOK:   true,
		},
		{
			name: "kind only, falls back to object name",
			labels: map[string]string{
				helpersv1.RelatedKindMetadataKey: "DaemonSet",
			},
			objName:  "node-agent-xyz",
			wantName: "daemonset-node-agent-xyz", // lower(kind)-cp.Name
			wantOK:   true,
		},
		{
			name:     "no kind label -> skip",
			labels:   map[string]string{helpersv1.RelatedNameMetadataKey: "nginx"},
			objName:  "replicaset-nginx-abc",
			wantName: "",
			wantOK:   false,
		},
		{
			name:     "nil labels -> skip",
			labels:   nil,
			objName:  "whatever",
			wantName: "",
			wantOK:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp := &softwarecomposition.ContainerProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name:   tt.objName,
					Labels: tt.labels,
				},
			}
			gotName, gotOK := workloadPolicyName(cp)
			assert.Equal(t, tt.wantOK, gotOK)
			assert.Equal(t, tt.wantName, gotName)
		})
	}
}
