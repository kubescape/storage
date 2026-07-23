package file

import (
	"context"
	"fmt"
	"testing"

	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/config"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
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
			tt.wantErr(t, a.PreSave(context.TODO(), tt.object), fmt.Sprintf("PreSave(%v)", tt.object))
			assert.Equal(t, tt.want, tt.object)
		})
	}
}

func TestNetworkNeighborhoodProcessor_PreSave_IPCollapse(t *testing.T) {
	const hostCount = 60
	ingress := make([]softwarecomposition.NetworkNeighbor, 0, hostCount)
	for i := 1; i <= hostCount; i++ {
		ingress = append(ingress, softwarecomposition.NetworkNeighbor{
			Identifier: fmt.Sprintf("external-%d", i),
			Type:       "external",
			IPAddress:  fmt.Sprintf("10.0.0.%d", i),
			Ports:      []softwarecomposition.NetworkPort{{Name: "80"}},
		})
	}
	profile := &softwarecomposition.NetworkNeighborhood{
		ObjectMeta: v1.ObjectMeta{Annotations: map[string]string{}},
		Spec: softwarecomposition.NetworkNeighborhoodSpec{
			Containers: []softwarecomposition.NetworkNeighborhoodContainer{
				{Name: "container1", Ingress: ingress},
			},
		},
	}

	a := NewNetworkNeighborhoodProcessor(config.Config{MaxNetworkNeighborhoodSize: 40000})
	a.SetCollapseSettings(func() dynamicpathdetector.CollapseSettings {
		return dynamicpathdetector.CollapseSettings{
			NetworkIPGroupThreshold: 10,
			NetworkCIDRFloorBits:    24,
		}
	})

	assert.NoError(t, a.PreSave(context.TODO(), profile))

	got := profile.Spec.Containers[0].Ingress
	assert.Len(t, got, 1, "expected all same-group host IPs to collapse into a single CIDR entry")
	assert.Empty(t, got[0].IPAddress)
	assert.Equal(t, []string{"10.0.0.0/26"}, got[0].IPAddresses)
	assert.Equal(t, []softwarecomposition.NetworkPort{{Name: "80"}}, got[0].Ports)
}
