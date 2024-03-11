package file

import (
	"fmt"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"testing"
)

func TestApplicationProfileProcessor_PreSave(t *testing.T) {
	tests := []struct {
		name    string
		object  runtime.Object
		want    runtime.Object
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "ApplicationProfile with initContainers",
			object: &softwarecomposition.ApplicationProfile{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{},
				},
				Spec: softwarecomposition.ApplicationProfileSpec{
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
						helpers.ResourceSizeMetadataKey: "5",
					},
				},
				Spec: softwarecomposition.ApplicationProfileSpec{
					InitContainers: []softwarecomposition.ApplicationProfileContainer{
						{
							Name:         "initContainer",
							Capabilities: []string{},
							Execs: []softwarecomposition.ExecCalls{
								{Path: "/bin/bash", Args: []string{"-c", "echo hello"}},
							},
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
			assert.Equal(t, tt.want, tt.object)
		})
	}
}
