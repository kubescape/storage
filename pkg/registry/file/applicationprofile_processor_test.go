package file

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/consts"
	"github.com/kubescape/storage/pkg/config"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var ap = softwarecomposition.ApplicationProfile{
	ObjectMeta: v1.ObjectMeta{
		Annotations: map[string]string{},
	},
	Spec: softwarecomposition.ApplicationProfileSpec{
		Architectures: []string{"amd64", "arm64", "amd64"},
		EphemeralContainers: []softwarecomposition.ApplicationProfileContainer{
			{
				Name: "ephemeralContainer",
				Execs: []softwarecomposition.ExecCalls{
					{Path: "/bin/bash", Args: []string{"-c", "echo abc"}},
				},
			},
		},
		InitContainers: []softwarecomposition.ApplicationProfileContainer{
			{
				Name: "initContainer",
				Execs: []softwarecomposition.ExecCalls{
					{Path: "/bin/bash", Args: []string{"-c", "echo hello"}},
				},
			},
		},
		Containers: []softwarecomposition.ApplicationProfileContainer{
			{
				Name: "container1",
				Execs: []softwarecomposition.ExecCalls{
					{Path: "/usr/bin/ls", Args: []string{"-l", "/tmp"}},
					{Path: "/usr/bin/ls", Args: []string{"-l", "/home"}},
					{Path: "/usr/bin/ls", Args: []string{"-l", "/tmp"}},
				},
			},
			{
				Name: "container2",
				Execs: []softwarecomposition.ExecCalls{
					{Path: "/usr/bin/ping", Args: []string{"localhost"}},
				},
				Opens: []softwarecomposition.OpenCalls{
					{Path: "/etc/hosts", Flags: []string{"O_CLOEXEC", "O_RDONLY"}},
				},
				Endpoints: []softwarecomposition.HTTPEndpoint{
					{
						Endpoint:  ":443/abc",
						Methods:   []string{"GET"},
						Internal:  false,
						Direction: consts.Inbound,
						Headers:   []byte{},
					},
				},
			},
		},
	},
}

func TestApplicationProfileProcessor_PreSave(t *testing.T) {
	tests := []struct {
		name                      string
		maxApplicationProfileSize int
		object                    runtime.Object
		want                      runtime.Object
		wantErr                   assert.ErrorAssertionFunc
	}{
		{
			name:                      "ApplicationProfile with initContainers and ephemeralContainers",
			maxApplicationProfileSize: 40000,
			object:                    &ap,
			want: &softwarecomposition.ApplicationProfile{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						helpers.ResourceSizeMetadataKey: "7",
					},
				},
				SchemaVersion: 1,
				Spec: softwarecomposition.ApplicationProfileSpec{
					Architectures: []string{"amd64", "arm64"},
					EphemeralContainers: []softwarecomposition.ApplicationProfileContainer{
						{
							Name: "ephemeralContainer",
							Execs: []softwarecomposition.ExecCalls{
								{Path: "/bin/bash", Args: []string{"-c", "echo abc"}},
							},
						},
					},
					InitContainers: []softwarecomposition.ApplicationProfileContainer{
						{
							Name: "initContainer",
							Execs: []softwarecomposition.ExecCalls{
								{Path: "/bin/bash", Args: []string{"-c", "echo hello"}},
							},
						},
					},
					Containers: []softwarecomposition.ApplicationProfileContainer{
						{
							Name: "container1",
							Execs: []softwarecomposition.ExecCalls{
								{Path: "/usr/bin/ls", Args: []string{"-l", "/tmp"}},
								{Path: "/usr/bin/ls", Args: []string{"-l", "/home"}},
							},
						},
						{
							Name: "container2",
							Execs: []softwarecomposition.ExecCalls{
								{Path: "/usr/bin/ping", Args: []string{"localhost"}},
							},
							Opens: []softwarecomposition.OpenCalls{
								{Path: "/etc/hosts", Flags: []string{"O_CLOEXEC", "O_RDONLY"}},
							},
							Endpoints: []softwarecomposition.HTTPEndpoint{
								{
									Endpoint:  ":443/abc",
									Methods:   []string{"GET"},
									Internal:  false,
									Direction: consts.Inbound,
									Headers:   []byte{},
								},
							},
						},
					},
				},
			},
			wantErr: assert.NoError,
		},
		{
			name:                      "ApplicationProfile too big",
			maxApplicationProfileSize: 5,
			object:                    &ap,
			want:                      &ap,
			wantErr:                   assert.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewApplicationProfileProcessor(config.Config{DefaultNamespace: "kubescape", MaxApplicationProfileSize: tt.maxApplicationProfileSize})
			tt.wantErr(t, a.PreSave(context.TODO(), tt.object), fmt.Sprintf("PreSave(%v)", tt.object))
			slices.Sort(tt.object.(*softwarecomposition.ApplicationProfile).Spec.Architectures)
			assert.Equal(t, tt.want, tt.object)
		})
	}
}

func TestDeflateRulePolicies(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]softwarecomposition.RulePolicy
		want map[string]softwarecomposition.RulePolicy
	}{
		{
			name: "nil map",
			in:   nil,
			want: nil,
		},
		{
			name: "empty map",
			in:   map[string]softwarecomposition.RulePolicy{},
			want: map[string]softwarecomposition.RulePolicy{},
		},
		{
			name: "single rule with unsorted processes",
			in: map[string]softwarecomposition.RulePolicy{
				"rule1": {
					AllowedProcesses: []string{"cat", "bash", "ls"},
					AllowedContainer: true,
				},
			},
			want: map[string]softwarecomposition.RulePolicy{
				"rule1": {
					AllowedProcesses: []string{"bash", "cat", "ls"},
					AllowedContainer: true,
				},
			},
		},
		{
			name: "multiple rules with duplicate processes",
			in: map[string]softwarecomposition.RulePolicy{
				"rule1": {
					AllowedProcesses: []string{"cat", "bash", "ls", "bash"},
					AllowedContainer: true,
				},
				"rule2": {
					AllowedProcesses: []string{"nginx", "nginx", "python"},
					AllowedContainer: false,
				},
			},
			want: map[string]softwarecomposition.RulePolicy{
				"rule1": {
					AllowedProcesses: []string{"bash", "cat", "ls"},
					AllowedContainer: true,
				},
				"rule2": {
					AllowedProcesses: []string{"nginx", "python"},
					AllowedContainer: false,
				},
			},
		},
		{
			name: "rule with empty processes",
			in: map[string]softwarecomposition.RulePolicy{
				"rule1": {
					AllowedProcesses: []string{},
					AllowedContainer: true,
				},
			},
			want: map[string]softwarecomposition.RulePolicy{
				"rule1": {
					AllowedProcesses: []string{},
					AllowedContainer: true,
				},
			},
		},
		{
			name: "rule with nil processes",
			in: map[string]softwarecomposition.RulePolicy{
				"rule1": {
					AllowedProcesses: nil,
					AllowedContainer: true,
				},
			},
			want: map[string]softwarecomposition.RulePolicy{
				"rule1": {
					AllowedProcesses: nil,
					AllowedContainer: true,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeflateRulePolicies(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}

// generateSOOpens creates N unique .so OpenCalls under /usr/lib/x86_64-linux-gnu/
func generateSOOpens(n int) []softwarecomposition.OpenCalls {
	opens := make([]softwarecomposition.OpenCalls, n)
	for i := 0; i < n; i++ {
		opens[i] = softwarecomposition.OpenCalls{
			Path:  fmt.Sprintf("/usr/lib/x86_64-linux-gnu/lib%d.so.%d", i, i%5),
			Flags: []string{"O_RDONLY", "O_CLOEXEC"},
		}
	}
	return opens
}

func TestDeflateApplicationProfileContainer_CollapsesManyOpens(t *testing.T) {
	opens := generateSOOpens(100)

	container := softwarecomposition.ApplicationProfileContainer{
		Name:  "test-container",
		Opens: opens,
	}

	result := deflateApplicationProfileContainer(container, nil)

	assert.Less(t, len(result.Opens), 100,
		"100 .so files should be collapsed, got %d opens", len(result.Opens))

	// Verify collapsed paths contain dynamic or wildcard segments
	for _, open := range result.Opens {
		if strings.HasPrefix(open.Path, "/usr/lib/x86_64-linux-gnu/") {
			assert.True(t,
				strings.Contains(open.Path, "\u22ef") || strings.Contains(open.Path, "*"),
				"path %q should contain a dynamic or wildcard segment", open.Path)
		}
	}

	// Flags should be preserved and merged
	for _, open := range result.Opens {
		assert.NotEmpty(t, open.Flags, "flags should be preserved after collapse")
	}
}

// Todo use the OpenDynamicThreshold in the test here not hardcoded integers
func TestDeflateApplicationProfileContainer_CollapsesWithSbomSet(t *testing.T) {
	opens := generateSOOpens(100)

	// Build sbomSet containing ALL the .so paths (realistic scenario)
	sbomSet := mapset.NewSet[string]()
	for _, open := range opens {
		sbomSet.Add(open.Path)
	}

	container := softwarecomposition.ApplicationProfileContainer{
		Name:  "test-container",
		Opens: opens,
	}

	result := deflateApplicationProfileContainer(container, sbomSet)

	// Even though all paths are in SBOM, they should still be collapsed
	assert.Less(t, len(result.Opens), 100,
		"SBOM paths should be collapsed too, got %d opens", len(result.Opens))
}

// Todo use the OpenDynamicThreshold in the test here not hardcoded integers
func TestDeflateApplicationProfileContainer_MixedPathsCollapse(t *testing.T) {
	var opens []softwarecomposition.OpenCalls

	for i := 0; i < 60; i++ {
		opens = append(opens, softwarecomposition.OpenCalls{
			Path:  fmt.Sprintf("/usr/lib/lib%d.so", i),
			Flags: []string{"O_RDONLY"},
		})
	}

	for i := 0; i < 55; i++ {
		opens = append(opens, softwarecomposition.OpenCalls{
			Path:  fmt.Sprintf("/etc/conf%d.cfg", i),
			Flags: []string{"O_RDONLY"},
		})
	}

	opens = append(opens,
		softwarecomposition.OpenCalls{Path: "/tmp/file1.txt", Flags: []string{"O_RDWR"}},
		softwarecomposition.OpenCalls{Path: "/tmp/file2.txt", Flags: []string{"O_RDWR"}},
	)

	container := softwarecomposition.ApplicationProfileContainer{
		Name:  "test-container",
		Opens: opens,
	}

	result := deflateApplicationProfileContainer(container, nil)

	// Count paths by prefix
	var usrLibPaths, etcPaths, tmpPaths int
	for _, open := range result.Opens {
		switch {
		case strings.HasPrefix(open.Path, "/usr/lib/"):
			usrLibPaths++
		case strings.HasPrefix(open.Path, "/etc/"):
			etcPaths++
		case strings.HasPrefix(open.Path, "/tmp/"):
			tmpPaths++
		}
	}

	assert.LessOrEqual(t, usrLibPaths, 1, "/usr/lib/ paths should collapse to 1, got %d", usrLibPaths)
	assert.LessOrEqual(t, etcPaths, 1, "/etc/ paths should collapse to 1, got %d", etcPaths)
	assert.Equal(t, 2, tmpPaths, "/tmp/ paths should remain individual (below threshold)")
}

// TestDeflateApplicationProfileContainer_NilSbomNoError verifies that nil sbomSet
// with a small number of opens (below threshold) works without error.
func TestDeflateApplicationProfileContainer_NilSbomNoError(t *testing.T) {
	container := softwarecomposition.ApplicationProfileContainer{
		Name: "test-container",
		Opens: []softwarecomposition.OpenCalls{
			{Path: "/etc/hosts", Flags: []string{"O_RDONLY"}},
			{Path: "/etc/resolv.conf", Flags: []string{"O_RDONLY"}},
			{Path: "/usr/lib/libc.so.6", Flags: []string{"O_RDONLY", "O_CLOEXEC"}},
		},
	}

	result := deflateApplicationProfileContainer(container, nil)

	// All 3 paths should remain (below any threshold)
	assert.Equal(t, 3, len(result.Opens), "paths below threshold should not collapse")
	// Paths should be sorted
	for i := 1; i < len(result.Opens); i++ {
		assert.True(t, result.Opens[i-1].Path <= result.Opens[i].Path,
			"opens should be sorted, got %q before %q", result.Opens[i-1].Path, result.Opens[i].Path)
	}
}

// TestDeflateApplicationProfileContainer_PreSaveEndToEnd verifies the full
// PreSave flow with an ApplicationProfile containing many opens that should collapse.
func TestDeflateApplicationProfileContainer_PreSaveEndToEnd(t *testing.T) {
	opens := generateSOOpens(100)

	profile := &softwarecomposition.ApplicationProfile{
		ObjectMeta: v1.ObjectMeta{
			Annotations: map[string]string{},
		},
		Spec: softwarecomposition.ApplicationProfileSpec{
			Containers: []softwarecomposition.ApplicationProfileContainer{
				{
					Name:  "main",
					Opens: opens,
				},
			},
		},
	}

	processor := NewApplicationProfileProcessor(config.Config{
		DefaultNamespace:          "kubescape",
		MaxApplicationProfileSize: 100000,
	})

	err := processor.PreSave(context.TODO(), profile)
	assert.NoError(t, err)

	// Todo use the OpenDynamicThreshold in the test here not hardcoded integers
	resultOpens := profile.Spec.Containers[0].Opens
	assert.Less(t, len(resultOpens), 100,
		"PreSave should collapse 100 .so files, got %d opens", len(resultOpens))

	// The collapsed path should contain dynamic or wildcard segments
	hasCollapsed := false
	for _, open := range resultOpens {
		if strings.Contains(open.Path, "\u22ef") || strings.Contains(open.Path, "*") {
			hasCollapsed = true
			break
		}
	}
	assert.True(t, hasCollapsed, "at least one path should contain a dynamic/wildcard segment after PreSave")
}
