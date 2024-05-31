package file

import (
	"fmt"
	"slices"
	"testing"

	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestApplicationProfileProcessor_PreSave(t *testing.T) {
	tests := []struct {
		name    string
		object  runtime.Object
		want    runtime.Object
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "ApplicationProfile with initContainers and ephemeralContainers",
			object: &softwarecomposition.ApplicationProfile{
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
						},
					},
				},
			},
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
						},
					},
				},
			},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := ApplicationProfileProcessor{}
			tt.wantErr(t, a.PreSave(tt.object), fmt.Sprintf("PreSave(%v)", tt.object))
			slices.Sort(tt.object.(*softwarecomposition.ApplicationProfile).Spec.Architectures)
			assert.Equal(t, tt.want, tt.object)
		})
	}
}
