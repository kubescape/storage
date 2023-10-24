package validation

import (
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func TestValidateNetworkNeighbors(t *testing.T) {
	port80 := int32(80)
	port1000000 := int32(1000000)
	tests := []struct {
		name             string
		port             int32
		networkNeighbors softwarecomposition.NetworkNeighbors
		expectedErrors   field.ErrorList
	}{
		{
			name: "valid",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				Spec: softwarecomposition.NetworkNeighborsSpec{
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							Identifier: "test",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     &port80,
									Name:     "UDP-80",
									Protocol: "UDP",
								},
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{},
		},
		{
			name: "invalid port name",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				Spec: softwarecomposition.NetworkNeighborsSpec{
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							Identifier: "test",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     &port80,
									Name:     "UDP",
									Protocol: "UDP",
								},
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec").Child("ingress").Index(0).Child("ports").Index(0).Child("name"), "UDP", "port name must be in the format {protocol}-{port}"),
			},
		},
		{
			name: "invalid port number",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				Spec: softwarecomposition.NetworkNeighborsSpec{
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							Identifier: "test",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     &port1000000,
									Name:     "UDP-1000000",
									Protocol: "UDP",
								},
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec").Child("ingress").Index(0).Child("ports").Index(0).Child("port"), int32(1000000), "port must be in range 0-65535"),
			},
		},
		{
			name: "invalid port number and name",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				Spec: softwarecomposition.NetworkNeighborsSpec{
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							Identifier: "test",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     &port1000000,
									Name:     "UDP-80",
									Protocol: "UDP",
								},
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec").Child("ingress").Index(0).Child("ports").Index(0).Child("port"), int32(1000000), "port must be in range 0-65535"),
				field.Invalid(field.NewPath("spec").Child("ingress").Index(0).Child("ports").Index(0).Child("name"), "UDP-80", "port name must be in the format {protocol}-{port}"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actualErrors := ValidateNetworkNeighbors(&test.networkNeighbors)
			assert.Equal(t, test.expectedErrors, actualErrors)
		})
	}
}
