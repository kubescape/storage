package networkneighborhood

import (
	"context"
	"reflect"
	"testing"

	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestPrepareForUpdate(t *testing.T) {
	tests := []struct {
		name           string
		oldAnnotations map[string]string
		newAnnotations map[string]string
		expected       map[string]string
	}{
		{
			name: "transition from complete (with status) to partial - rejected",
			oldAnnotations: map[string]string{
				helpers.CompletionMetadataKey: "complete",
				helpers.StatusMetadataKey:     "initializing",
			},
			newAnnotations: map[string]string{
				helpers.CompletionMetadataKey: "partial",
				helpers.StatusMetadataKey:     "ready",
			},
			expected: map[string]string{
				helpers.CompletionMetadataKey: "complete",
				helpers.StatusMetadataKey:     "initializing",
			},
		},
		{
			name: "transition from partial (with status) to complete - accepted",
			oldAnnotations: map[string]string{
				helpers.CompletionMetadataKey: "partial",
				helpers.StatusMetadataKey:     "initializing",
			},
			newAnnotations: map[string]string{
				helpers.CompletionMetadataKey: "partial",
				helpers.StatusMetadataKey:     "ready",
			},
			expected: map[string]string{
				helpers.CompletionMetadataKey: "partial",
				helpers.StatusMetadataKey:     "ready",
			},
		},
		{
			name: "transition from partial (without status) to complete - accepted",
			oldAnnotations: map[string]string{
				helpers.CompletionMetadataKey: "partial",
			},
			newAnnotations: map[string]string{
				helpers.CompletionMetadataKey: "complete",
				helpers.StatusMetadataKey:     "ready",
			},
			expected: map[string]string{
				helpers.CompletionMetadataKey: "complete",
				helpers.StatusMetadataKey:     "ready",
			},
		},
		{
			name: "transition from complete (without status) to partial - rejected",
			oldAnnotations: map[string]string{
				helpers.CompletionMetadataKey: "complete",
			},
			newAnnotations: map[string]string{
				helpers.CompletionMetadataKey: "partial",
				helpers.StatusMetadataKey:     "initializing",
			},
			expected: map[string]string{
				helpers.CompletionMetadataKey: "complete",
			},
		},
		{
			name: "transition from a final AP - all changes are rejected",
			oldAnnotations: map[string]string{
				helpers.CompletionMetadataKey: "complete",
				helpers.StatusMetadataKey:     "completed",
			},
			newAnnotations: map[string]string{
				helpers.CompletionMetadataKey: "partial",
				helpers.StatusMetadataKey:     "initializing",
			},
			expected: map[string]string{
				helpers.CompletionMetadataKey: "complete",
				helpers.StatusMetadataKey:     "completed",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NetworkNeighborhoodStrategy{}

			obj := &softwarecomposition.NetworkNeighborhood{ObjectMeta: metav1.ObjectMeta{Annotations: tt.newAnnotations}}
			old := &softwarecomposition.NetworkNeighborhood{ObjectMeta: metav1.ObjectMeta{Annotations: tt.oldAnnotations}}

			s.PrepareForUpdate(context.Background(), obj, old)
			if !reflect.DeepEqual(obj.Annotations, tt.expected) {
				t.Errorf("PrepareForUpdate() = %v, want %v", obj.Annotations, tt.expected)
			}
		})
	}
}

func TestPrepareForUpdateFullObj(t *testing.T) {
	tests := []struct {
		name     string
		old      *softwarecomposition.NetworkNeighborhood
		new      *softwarecomposition.NetworkNeighborhood
		expected *softwarecomposition.NetworkNeighborhood
	}{
		{
			name: "transition from initializing to ready - changes are accepted",
			old: &softwarecomposition.NetworkNeighborhood{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						helpers.CompletionMetadataKey: "complete",
						helpers.StatusMetadataKey:     "initializing",
					},
				},
				Spec: softwarecomposition.NetworkNeighborhoodSpec{
					Containers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Name:   "container1",
							Egress: []softwarecomposition.NetworkNeighbor{},
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "154.53.46.32",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(80)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-80",
										},
									},
								},
							},
						},
					},
				},
			},
			new: &softwarecomposition.NetworkNeighborhood{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						helpers.CompletionMetadataKey: "complete",
						helpers.StatusMetadataKey:     "ready",
					},
				},
				Spec: softwarecomposition.NetworkNeighborhoodSpec{
					Containers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Name:   "container1",
							Egress: []softwarecomposition.NetworkNeighbor{},
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "154.53.46.32",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(80)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-80",
										},
									},
								},
							},
						},
						{
							Name:   "container2",
							Egress: []softwarecomposition.NetworkNeighbor{},
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "154.53.46.32",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(80)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-80",
										},
									},
								},
							},
						},
					},
				},
			},
			expected: &softwarecomposition.NetworkNeighborhood{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						helpers.CompletionMetadataKey: "complete",
						helpers.StatusMetadataKey:     "ready",
					},
				},
				Spec: softwarecomposition.NetworkNeighborhoodSpec{
					Containers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Name:   "container1",
							Egress: []softwarecomposition.NetworkNeighbor{},
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "154.53.46.32",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(80)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-80",
										},
									},
								},
							},
						},
						{
							Name:   "container2",
							Egress: []softwarecomposition.NetworkNeighbor{},
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "154.53.46.32",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(80)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-80",
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "transition from a final AP - all changes are rejected",
			old: &softwarecomposition.NetworkNeighborhood{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						helpers.CompletionMetadataKey: "complete",
						helpers.StatusMetadataKey:     "completed",
					},
				},
				Spec: softwarecomposition.NetworkNeighborhoodSpec{
					Containers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Name:   "container1",
							Egress: []softwarecomposition.NetworkNeighbor{},
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "154.53.46.32",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(80)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-80",
										},
									},
								},
							},
						},
					},
				},
			},
			new: &softwarecomposition.NetworkNeighborhood{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						helpers.CompletionMetadataKey: "partial",
						helpers.StatusMetadataKey:     "initializing",
					},
				},
				Spec: softwarecomposition.NetworkNeighborhoodSpec{
					Containers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Name:   "container1",
							Egress: []softwarecomposition.NetworkNeighbor{},
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "154.53.46.32",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(80)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-80",
										},
									},
								},
							},
						},
						{
							Name:   "container2",
							Egress: []softwarecomposition.NetworkNeighbor{},
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "154.53.46.32",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(80)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-80",
										},
									},
								},
							},
						},
					},
				},
			},
			expected: &softwarecomposition.NetworkNeighborhood{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						helpers.CompletionMetadataKey: "complete",
						helpers.StatusMetadataKey:     "completed",
					},
				},
				Spec: softwarecomposition.NetworkNeighborhoodSpec{
					Containers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Name:   "container1",
							Egress: []softwarecomposition.NetworkNeighbor{},
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "154.53.46.32",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(80)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-80",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NetworkNeighborhoodStrategy{}
			s.PrepareForUpdate(context.Background(), tt.new, tt.old)
			if !reflect.DeepEqual(tt.new, tt.expected) {
				t.Errorf("PrepareForUpdate() = %v, want %v", tt.new, tt.expected)
			}
		})
	}
}
