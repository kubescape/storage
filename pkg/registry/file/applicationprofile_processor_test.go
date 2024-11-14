package file

import (
	"fmt"
	"slices"
	"strconv"
	"testing"

	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/consts"
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
			maxApplicationProfileSize: DefaultMaxApplicationProfileSize,
			object:                    &ap,
			want: &softwarecomposition.ApplicationProfile{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						helpers.ResourceSizeMetadataKey: "6",
					},
				},
				Spec: softwarecomposition.ApplicationProfileSpec{
					Architectures: []string{"amd64", "arm64"},
					EphemeralContainers: []softwarecomposition.ApplicationProfileContainer{
						{
							Name:         "ephemeralContainer",
							Capabilities: []string{},
							Execs: []softwarecomposition.ExecCalls{
								{Path: "/bin/bash", Args: []string{"-c", "echo abc"}},
							},
							Opens:    []softwarecomposition.OpenCalls{},
							Syscalls: []string{},
						},
					},
					InitContainers: []softwarecomposition.ApplicationProfileContainer{
						{
							Name:         "initContainer",
							Capabilities: []string{},
							Execs: []softwarecomposition.ExecCalls{
								{Path: "/bin/bash", Args: []string{"-c", "echo hello"}},
							},
							Opens:    []softwarecomposition.OpenCalls{},
							Syscalls: []string{},
						},
					},
					Containers: []softwarecomposition.ApplicationProfileContainer{
						{
							Name:         "container1",
							Capabilities: []string{},
							Execs: []softwarecomposition.ExecCalls{
								{Path: "/usr/bin/ls", Args: []string{"-l", "/tmp"}},
								{Path: "/usr/bin/ls", Args: []string{"-l", "/home"}},
							},
							Opens:    []softwarecomposition.OpenCalls{},
							Syscalls: []string{},
						},
						{
							Name:         "container2",
							Capabilities: []string{},
							Execs: []softwarecomposition.ExecCalls{
								{Path: "/usr/bin/ping", Args: []string{"localhost"}},
							},
							Opens: []softwarecomposition.OpenCalls{
								{Path: "/etc/hosts", Flags: []string{"O_CLOEXEC", "O_RDONLY"}},
							},
							Syscalls: []string{},
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
			t.Setenv("MAX_APPLICATION_PROFILE_SIZE", strconv.Itoa(tt.maxApplicationProfileSize))
			a := NewApplicationProfileProcessor()
			tt.wantErr(t, a.PreSave(tt.object), fmt.Sprintf("PreSave(%v)", tt.object))
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
					AllowedProcesses: []string{},
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
