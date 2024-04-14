package file

import (
	"fmt"
	"testing"

	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestNetworkNeighborhoodProcessor_PreSave(t *testing.T) {
	tests := []struct {
		name    string
		object  runtime.Object
		want    runtime.Object
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "NetworkNeighborhood with initContainers and ephemeralContainers",
			object: &softwarecomposition.NetworkNeighborhood{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{},
				},
				Spec: softwarecomposition.NetworkNeighborhoodSpec{
					EphemeralContainers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Name: "ephemeralContainer",
							Ingress: []softwarecomposition.NetworkNeighbor{
								{Identifier: "a", Ports: []softwarecomposition.NetworkPort{{Name: "80"}}},
								{Identifier: "b", Ports: []softwarecomposition.NetworkPort{{Name: "80"}}},
								{Identifier: "a", Ports: []softwarecomposition.NetworkPort{{Name: "443"}, {Name: "80"}}},
							},
						},
					},
					InitContainers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Name: "initContainer",
							Ingress: []softwarecomposition.NetworkNeighbor{
								{Identifier: "a", Ports: []softwarecomposition.NetworkPort{{Name: "80"}}},
								{Identifier: "a", Ports: []softwarecomposition.NetworkPort{{Name: "80"}}},
							},
						},
					},
					Containers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Name: "container1",
							Ingress: []softwarecomposition.NetworkNeighbor{
								{Identifier: "a", Ports: []softwarecomposition.NetworkPort{{Name: "80"}}},
								{Identifier: "c", Ports: []softwarecomposition.NetworkPort{{Name: "8080"}}},
							},
						},
						{
							Name: "container2",
							Ingress: []softwarecomposition.NetworkNeighbor{
								{Identifier: "a", Ports: []softwarecomposition.NetworkPort{{Name: "80"}}},
							},
						},
					},
				},
			},
			want: &softwarecomposition.NetworkNeighborhood{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						helpers.ResourceSizeMetadataKey: "6",
					},
				},
				Spec: softwarecomposition.NetworkNeighborhoodSpec{
					EphemeralContainers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Name: "ephemeralContainer",
							Ingress: []softwarecomposition.NetworkNeighbor{
								{Identifier: "a", Ports: []softwarecomposition.NetworkPort{{Name: "80"}, {Name: "443"}}, DNSNames: []string{}},
								{Identifier: "b", Ports: []softwarecomposition.NetworkPort{{Name: "80"}}},
							},
							Egress: []softwarecomposition.NetworkNeighbor{},
						},
					},
					InitContainers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Name: "initContainer",
							Ingress: []softwarecomposition.NetworkNeighbor{
								{Identifier: "a", Ports: []softwarecomposition.NetworkPort{{Name: "80"}}, DNSNames: []string{}},
							},
							Egress: []softwarecomposition.NetworkNeighbor{},
						},
					},
					Containers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Name: "container1",
							Ingress: []softwarecomposition.NetworkNeighbor{
								{Identifier: "a", Ports: []softwarecomposition.NetworkPort{{Name: "80"}}},
								{Identifier: "c", Ports: []softwarecomposition.NetworkPort{{Name: "8080"}}},
							},
							Egress: []softwarecomposition.NetworkNeighbor{},
						},
						{
							Name: "container2",
							Ingress: []softwarecomposition.NetworkNeighbor{
								{Identifier: "a", Ports: []softwarecomposition.NetworkPort{{Name: "80"}}},
							},
							Egress: []softwarecomposition.NetworkNeighbor{},
						},
					},
				},
			},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NetworkNeighborhoodProcessor{}
			tt.wantErr(t, a.PreSave(tt.object), fmt.Sprintf("PreSave(%v)", tt.object))
			assert.Equal(t, tt.want, tt.object)
		})
	}
}
