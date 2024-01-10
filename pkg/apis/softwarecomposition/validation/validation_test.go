package validation

import (
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
)

func TestValidateNetworkNeighbors(t *testing.T) {
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
									Port:     ptr.To(int32(80)),
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
									Port:     ptr.To(int32(80)),
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
									Port:     ptr.To(int32(1000000)),
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
									Port:     ptr.To(int32(1000000)),
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

func TestValidateKnownServer(t *testing.T) {
	tests := []struct {
		name           string
		knownServer    softwarecomposition.KnownServer
		expectedErrors field.ErrorList
	}{
		{
			name: "valid",
			knownServer: softwarecomposition.KnownServer{
				Spec: []softwarecomposition.KnownServerEntry{
					{
						IPBlock: "1.2.3.4/32",
						Name:    "test",
						Server:  "test.com",
					},
				},
			},
			expectedErrors: field.ErrorList{},
		},
		{
			name: "no ipBlock",
			knownServer: softwarecomposition.KnownServer{
				Spec: []softwarecomposition.KnownServerEntry{
					{
						Name:   "test",
						Server: "test.com",
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec").Child("entries").Index(0).Child("ipBlock"), "", "ipBlock must be set"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actualErrors := ValidateKnownServer(&test.knownServer)
			assert.Equal(t, test.expectedErrors, actualErrors)
		})
	}

}
