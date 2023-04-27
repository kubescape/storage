package fake

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/go-test/deep"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestFakeSBOMSPDXv2p3Filtereds_Patch(t *testing.T) {
	tests := []struct {
		name       string
		existing   *v1beta1.SBOMSPDXv2p3Filtered
		patch      *v1beta1.SBOMSPDXv2p3Filtered
		wantResult *v1beta1.SBOMSPDXv2p3Filtered
		wantErr    bool
	}{
		{
			name: "empty patch",
			existing: &v1beta1.SBOMSPDXv2p3Filtered{
				ObjectMeta: v1.ObjectMeta{
					Name: "test",
				},
				Spec: v1beta1.SBOMSPDXv2p3Spec{
					SPDX: v1beta1.Document{
						Packages: []*v1beta1.Package{
							{PackageName: "package 1"},
							{PackageName: "package 2"},
						},
					},
				},
			},
			patch: &v1beta1.SBOMSPDXv2p3Filtered{},
			wantResult: &v1beta1.SBOMSPDXv2p3Filtered{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Spec: v1beta1.SBOMSPDXv2p3Spec{
					SPDX: v1beta1.Document{
						Packages: []*v1beta1.Package{
							{PackageName: "package 1"},
							{PackageName: "package 2"},
						},
					},
				},
			},
		},
		{
			name: "add package",
			existing: &v1beta1.SBOMSPDXv2p3Filtered{
				ObjectMeta: v1.ObjectMeta{
					Name: "test",
				},
				Spec: v1beta1.SBOMSPDXv2p3Spec{
					SPDX: v1beta1.Document{
						Packages: []*v1beta1.Package{
							{PackageName: "package 1"},
							{PackageName: "package 2"},
						},
					},
				},
			},
			patch: &v1beta1.SBOMSPDXv2p3Filtered{
				Spec: v1beta1.SBOMSPDXv2p3Spec{
					SPDX: v1beta1.Document{
						Packages: []*v1beta1.Package{
							{PackageName: "package 3"},
						},
					},
				},
			},
			wantResult: &v1beta1.SBOMSPDXv2p3Filtered{
				ObjectMeta: v1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Spec: v1beta1.SBOMSPDXv2p3Spec{
					SPDX: v1beta1.Document{
						Packages: []*v1beta1.Package{
							{PackageName: "package 1"},
							{PackageName: "package 2"},
							{PackageName: "package 3"},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewSimpleClientset().SpdxV1beta1().SBOMSPDXv2p3Filtereds("default")
			_, _ = c.Create(context.TODO(), tt.existing, v1.CreateOptions{})
			bytes, _ := json.Marshal(tt.patch)
			gotResult, err := c.Patch(context.TODO(), tt.existing.Name, types.StrategicMergePatchType, bytes, v1.PatchOptions{})
			if (err != nil) != tt.wantErr {
				t.Errorf("Patch() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			diff := deep.Equal(gotResult, tt.wantResult)
			if diff != nil {
				t.Errorf("compare failed: %v", diff)
			}
		})
	}
}
