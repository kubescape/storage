package file

import (
	"context"
	"fmt"
	"testing"

	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/config"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var nn = softwarecomposition.NetworkNeighborhood{
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
					{Identifier: "c", Ports: []softwarecomposition.NetworkPort{{Name: "80"}}},
					{Identifier: "c", Ports: []softwarecomposition.NetworkPort{{Name: "80"}}},
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
}

func TestNetworkNeighborhoodProcessor_PreSave(t *testing.T) {
	tests := []struct {
		name                       string
		maxNetworkNeighborhoodSize int
		object                     runtime.Object
		want                       runtime.Object
		wantErr                    assert.ErrorAssertionFunc
	}{
		{
			name:                       "NetworkNeighborhood with initContainers and ephemeralContainers",
			maxNetworkNeighborhoodSize: 40000,
			object:                     &nn,
			want: &softwarecomposition.NetworkNeighborhood{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						helpers.ResourceSizeMetadataKey: "7",
					},
				},
				SchemaVersion: 1,
				Spec: softwarecomposition.NetworkNeighborhoodSpec{
					EphemeralContainers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Name: "ephemeralContainer",
							Ingress: []softwarecomposition.NetworkNeighbor{
								{Identifier: "a", Ports: []softwarecomposition.NetworkPort{{Name: "80"}, {Name: "443"}}},
								{Identifier: "b", Ports: []softwarecomposition.NetworkPort{{Name: "80"}}},
								{Identifier: "c", Ports: []softwarecomposition.NetworkPort{{Name: "80"}}},
							},
						},
					},
					InitContainers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Name: "initContainer",
							Ingress: []softwarecomposition.NetworkNeighbor{
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
			wantErr: assert.NoError,
		},
		{
			name:                       "NetworkNeighborhood too big",
			maxNetworkNeighborhoodSize: 5,
			object:                     &nn,
			want:                       &nn,
			wantErr:                    assert.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewNetworkNeighborhoodProcessor(config.Config{MaxNetworkNeighborhoodSize: tt.maxNetworkNeighborhoodSize})
			tt.wantErr(t, a.PreSave(context.TODO(), nil, tt.object), fmt.Sprintf("PreSave(%v)", tt.object))
			assert.Equal(t, tt.want, tt.object)
		})
	}
}
