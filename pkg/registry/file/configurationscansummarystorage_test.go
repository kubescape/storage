package file

import (
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGenerateConfigurationScanSummary(t *testing.T) {
	tests := []struct {
		name                           string
		wlConfigurationScanSummaryList softwarecomposition.WorkloadConfigurationScanSummaryList
		want                           softwarecomposition.ConfigurationScanSummary
	}{
		{
			name:                           "no resources",
			wlConfigurationScanSummaryList: softwarecomposition.WorkloadConfigurationScanSummaryList{},
			want:                           softwarecomposition.ConfigurationScanSummary{},
		},
		{
			name: "one resource",
			wlConfigurationScanSummaryList: softwarecomposition.WorkloadConfigurationScanSummaryList{
				Items: []softwarecomposition.WorkloadConfigurationScanSummary{
					{
						TypeMeta: v1.TypeMeta{
							Kind:       "WorkloadConfigurationScanSummary",
							APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
						},
						ObjectMeta: v1.ObjectMeta{
							Name:      "workload-1",
							Namespace: "default",
						},
						Spec: softwarecomposition.WorkloadConfigurationScanSummarySpec{
							Severities: softwarecomposition.WorkloadConfigurationScanSeveritiesSummary{
								Critical: 0,
								High:     1,
								Medium:   1,
								Low:      2,
							},
						},
					},
				},
			},
			want: softwarecomposition.ConfigurationScanSummary{
				TypeMeta: v1.TypeMeta{
					Kind:       "ConfigurationScanSummary",
					APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
				},
				ObjectMeta: v1.ObjectMeta{
					Name: "default",
				},
				Spec: softwarecomposition.ConfigurationScanSummarySpec{
					Severities: softwarecomposition.WorkloadConfigurationScanSeveritiesSummary{
						Critical: 0,
						High:     1,
						Medium:   1,
						Low:      2,
					},
					WorkloadConfigurationScanSummaryIdentifiers: []softwarecomposition.WorkloadConfigurationScanSummaryIdentifier{
						{
							Namespace: "default",
							Kind:      "WorkloadConfigurationScanSummary",
							Name:      "workload-1",
						},
					},
				},
			},
		},

		{
			name: "multiple resources",
			wlConfigurationScanSummaryList: softwarecomposition.WorkloadConfigurationScanSummaryList{
				Items: []softwarecomposition.WorkloadConfigurationScanSummary{
					{
						TypeMeta: v1.TypeMeta{
							Kind:       "WorkloadConfigurationScanSummary",
							APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
						},
						ObjectMeta: v1.ObjectMeta{
							Name:      "workload-1",
							Namespace: "default",
						},
						Spec: softwarecomposition.WorkloadConfigurationScanSummarySpec{
							Severities: softwarecomposition.WorkloadConfigurationScanSeveritiesSummary{
								Critical: 0,
								High:     1,
								Medium:   1,
								Low:      2,
							},
						},
					},
					{
						TypeMeta: v1.TypeMeta{
							Kind:       "WorkloadConfigurationScanSummary",
							APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
						},
						ObjectMeta: v1.ObjectMeta{
							Name:      "workload-2",
							Namespace: "default",
						},
						Spec: softwarecomposition.WorkloadConfigurationScanSummarySpec{
							Severities: softwarecomposition.WorkloadConfigurationScanSeveritiesSummary{
								Critical: 1,
								High:     1,
								Medium:   1,
								Low:      1,
							},
						},
					},
					{
						TypeMeta: v1.TypeMeta{
							Kind:       "WorkloadConfigurationScanSummary",
							APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
						},
						ObjectMeta: v1.ObjectMeta{
							Name:      "workload-3",
							Namespace: "default",
						},
						Spec: softwarecomposition.WorkloadConfigurationScanSummarySpec{
							Severities: softwarecomposition.WorkloadConfigurationScanSeveritiesSummary{
								Critical: 1,
								High:     1,
								Low:      1,
							},
						},
					},
				},
			},

			want: softwarecomposition.ConfigurationScanSummary{
				TypeMeta: v1.TypeMeta{
					Kind:       "ConfigurationScanSummary",
					APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
				},
				ObjectMeta: v1.ObjectMeta{
					Name: "default",
				},
				Spec: softwarecomposition.ConfigurationScanSummarySpec{
					Severities: softwarecomposition.WorkloadConfigurationScanSeveritiesSummary{
						Critical: 2,
						High:     3,
						Medium:   2,
						Low:      4,
					},
					WorkloadConfigurationScanSummaryIdentifiers: []softwarecomposition.WorkloadConfigurationScanSummaryIdentifier{
						{
							Namespace: "default",
							Kind:      "WorkloadConfigurationScanSummary",
							Name:      "workload-1",
						},
						{
							Namespace: "default",
							Kind:      "WorkloadConfigurationScanSummary",
							Name:      "workload-2",
						},
						{
							Namespace: "default",
							Kind:      "WorkloadConfigurationScanSummary",
							Name:      "workload-3",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateConfigurationScanSummary(tt.wlConfigurationScanSummaryList)

			if got.APIVersion != tt.want.APIVersion {
				t.Errorf("generateConfigurationScanSummary() = %v, want %v", got.APIVersion, tt.want.APIVersion)
			}

			if got.Kind != tt.want.Kind {
				t.Errorf("generateConfigurationScanSummary() = %v, want %v", got.Kind, tt.want.Kind)
			}

			if got.Name != tt.want.Name {
				t.Errorf("generateConfigurationScanSummary() = %v, want %v", got.Name, tt.want.Name)
			}

			if got.Spec.Severities.Critical != tt.want.Spec.Severities.Critical {
				t.Errorf("generateConfigurationScanSummary() = %v, want %v", got.Spec.Severities.Critical, tt.want.Spec.Severities.Critical)
			}

			if got.Spec.Severities.High != tt.want.Spec.Severities.High {
				t.Errorf("generateConfigurationScanSummary() = %v, want %v", got.Spec.Severities.High, tt.want.Spec.Severities.High)
			}

			if got.Spec.Severities.Medium != tt.want.Spec.Severities.Medium {
				t.Errorf("generateConfigurationScanSummary() = %v, want %v", got.Spec.Severities.Medium, tt.want.Spec.Severities.Medium)
			}

			if got.Spec.Severities.Low != tt.want.Spec.Severities.Low {
				t.Errorf("generateConfigurationScanSummary() = %v, want %v", got.Spec.Severities.Low, tt.want.Spec.Severities.Low)
			}

			if got.Spec.Severities.Unknown != tt.want.Spec.Severities.Unknown {
				t.Errorf("generateConfigurationScanSummary() = %v, want %v", got.Spec.Severities.Unknown, tt.want.Spec.Severities.Unknown)
			}

			if len(got.Spec.WorkloadConfigurationScanSummaryIdentifiers) != len(tt.want.Spec.WorkloadConfigurationScanSummaryIdentifiers) {
				t.Errorf("generateConfigurationScanSummary() = %v, want %v", len(got.Spec.WorkloadConfigurationScanSummaryIdentifiers), len(tt.want.Spec.WorkloadConfigurationScanSummaryIdentifiers))
			}

			for i := range got.Spec.WorkloadConfigurationScanSummaryIdentifiers {
				found := false
				for j := range tt.want.Spec.WorkloadConfigurationScanSummaryIdentifiers {
					if got.Spec.WorkloadConfigurationScanSummaryIdentifiers[i].Name == tt.want.Spec.WorkloadConfigurationScanSummaryIdentifiers[j].Name {
						found = true
					}
				}
				assert.Equal(t, true, found)
			}
		})
	}
}

func TestGenerateConfigurationScanSummaryForCluster(t *testing.T) {
	test := []struct {
		name                           string
		wlConfigurationScanSummaryList softwarecomposition.WorkloadConfigurationScanSummaryList
		expected                       softwarecomposition.ConfigurationScanSummaryList
	}{
		{
			name:                           "no resources",
			wlConfigurationScanSummaryList: softwarecomposition.WorkloadConfigurationScanSummaryList{},
			expected:                       softwarecomposition.ConfigurationScanSummaryList{},
		},
		{
			name: "one resource",
			wlConfigurationScanSummaryList: softwarecomposition.WorkloadConfigurationScanSummaryList{
				Items: []softwarecomposition.WorkloadConfigurationScanSummary{
					{
						TypeMeta: v1.TypeMeta{
							Kind:       "WorkloadConfigurationScanSummary",
							APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
						},
						ObjectMeta: v1.ObjectMeta{
							Name:      "workload-1",
							Namespace: "default",
						},
						Spec: softwarecomposition.WorkloadConfigurationScanSummarySpec{
							Severities: softwarecomposition.WorkloadConfigurationScanSeveritiesSummary{
								Critical: 0,
								High:     1,
								Medium:   1,
								Low:      2,
							},
						},
					},
				},
			},
			expected: softwarecomposition.ConfigurationScanSummaryList{
				TypeMeta: v1.TypeMeta{
					Kind:       "ConfigurationScanSummaryList",
					APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
				},
				Items: []softwarecomposition.ConfigurationScanSummary{
					{
						TypeMeta: v1.TypeMeta{
							Kind:       "ConfigurationScanSummary",
							APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
						},
						ObjectMeta: v1.ObjectMeta{
							Name: "default",
						},
						Spec: softwarecomposition.ConfigurationScanSummarySpec{
							Severities: softwarecomposition.WorkloadConfigurationScanSeveritiesSummary{
								Critical: 0,
								High:     1,
								Medium:   1,
								Low:      2,
							},
							WorkloadConfigurationScanSummaryIdentifiers: []softwarecomposition.WorkloadConfigurationScanSummaryIdentifier{
								{
									Namespace: "default",
									Kind:      "WorkloadConfigurationScanSummary",
									Name:      "workload-1",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "two resources same namespace",
			wlConfigurationScanSummaryList: softwarecomposition.WorkloadConfigurationScanSummaryList{
				Items: []softwarecomposition.WorkloadConfigurationScanSummary{
					{
						TypeMeta: v1.TypeMeta{
							Kind:       "WorkloadConfigurationScanSummary",
							APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
						},
						ObjectMeta: v1.ObjectMeta{
							Name:      "workload-1",
							Namespace: "default",
						},
						Spec: softwarecomposition.WorkloadConfigurationScanSummarySpec{
							Severities: softwarecomposition.WorkloadConfigurationScanSeveritiesSummary{
								Critical: 0,
								High:     1,
								Medium:   1,
								Low:      2,
							},
						},
					},
					{
						TypeMeta: v1.TypeMeta{
							Kind:       "WorkloadConfigurationScanSummary",
							APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
						},
						ObjectMeta: v1.ObjectMeta{
							Name:      "workload-2",
							Namespace: "default",
						},
						Spec: softwarecomposition.WorkloadConfigurationScanSummarySpec{
							Severities: softwarecomposition.WorkloadConfigurationScanSeveritiesSummary{
								Critical: 1,
								High:     2,
								Medium:   3,
								Low:      4,
							},
						},
					},
				},
			},
			expected: softwarecomposition.ConfigurationScanSummaryList{
				TypeMeta: v1.TypeMeta{
					Kind:       "ConfigurationScanSummaryList",
					APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
				},
				Items: []softwarecomposition.ConfigurationScanSummary{
					{
						TypeMeta: v1.TypeMeta{
							Kind:       "ConfigurationScanSummary",
							APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
						},
						ObjectMeta: v1.ObjectMeta{
							Name: "default",
						},
						Spec: softwarecomposition.ConfigurationScanSummarySpec{
							Severities: softwarecomposition.WorkloadConfigurationScanSeveritiesSummary{
								Critical: 1,
								High:     3,
								Medium:   4,
								Low:      6,
							},
							WorkloadConfigurationScanSummaryIdentifiers: []softwarecomposition.WorkloadConfigurationScanSummaryIdentifier{
								{
									Namespace: "default",
									Kind:      "WorkloadConfigurationScanSummary",
									Name:      "workload-1",
								},
								{
									Namespace: "default",
									Kind:      "WorkloadConfigurationScanSummary",
									Name:      "workload-2",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "multiple resources different namespaces",
			wlConfigurationScanSummaryList: softwarecomposition.WorkloadConfigurationScanSummaryList{
				Items: []softwarecomposition.WorkloadConfigurationScanSummary{
					{
						TypeMeta: v1.TypeMeta{
							Kind:       "WorkloadConfigurationScanSummary",
							APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
						},
						ObjectMeta: v1.ObjectMeta{
							Name:      "workload-1",
							Namespace: "default",
						},
						Spec: softwarecomposition.WorkloadConfigurationScanSummarySpec{
							Severities: softwarecomposition.WorkloadConfigurationScanSeveritiesSummary{
								Critical: 0,
								High:     1,
								Medium:   1,
								Low:      2,
							},
						},
					},
					{
						TypeMeta: v1.TypeMeta{
							Kind:       "WorkloadConfigurationScanSummary",
							APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
						},
						ObjectMeta: v1.ObjectMeta{
							Name:      "workload-2",
							Namespace: "default",
						},
						Spec: softwarecomposition.WorkloadConfigurationScanSummarySpec{
							Severities: softwarecomposition.WorkloadConfigurationScanSeveritiesSummary{
								Critical: 1,
								High:     2,
								Medium:   3,
								Low:      4,
							},
						},
					},
					{
						TypeMeta: v1.TypeMeta{
							Kind:       "WorkloadConfigurationScanSummary",
							APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
						},
						ObjectMeta: v1.ObjectMeta{
							Name:      "workload-3",
							Namespace: "wardle",
						},
						Spec: softwarecomposition.WorkloadConfigurationScanSummarySpec{
							Severities: softwarecomposition.WorkloadConfigurationScanSeveritiesSummary{
								Critical: 0,
								High:     1,
								Medium:   1,
								Low:      2,
							},
						},
					},
					{
						TypeMeta: v1.TypeMeta{
							Kind:       "WorkloadConfigurationScanSummary",
							APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
						},
						ObjectMeta: v1.ObjectMeta{
							Name:      "workload-4",
							Namespace: "wardle",
						},
						Spec: softwarecomposition.WorkloadConfigurationScanSummarySpec{
							Severities: softwarecomposition.WorkloadConfigurationScanSeveritiesSummary{
								Critical: 3,
								High:     3,
								Medium:   2,
								Low:      2,
							},
						},
					},
				},
			},
			expected: softwarecomposition.ConfigurationScanSummaryList{
				TypeMeta: v1.TypeMeta{
					Kind:       "ConfigurationScanSummaryList",
					APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
				},
				Items: []softwarecomposition.ConfigurationScanSummary{
					{
						TypeMeta: v1.TypeMeta{
							Kind:       "ConfigurationScanSummary",
							APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
						},
						ObjectMeta: v1.ObjectMeta{
							Name: "default",
						},
						Spec: softwarecomposition.ConfigurationScanSummarySpec{
							Severities: softwarecomposition.WorkloadConfigurationScanSeveritiesSummary{
								Critical: 1,
								High:     3,
								Medium:   4,
								Low:      6,
							},
							WorkloadConfigurationScanSummaryIdentifiers: []softwarecomposition.WorkloadConfigurationScanSummaryIdentifier{
								{
									Namespace: "default",
									Kind:      "WorkloadConfigurationScanSummary",
									Name:      "workload-1",
								},
								{
									Namespace: "default",
									Kind:      "WorkloadConfigurationScanSummary",
									Name:      "workload-2",
								},
							},
						},
					},
					{
						TypeMeta: v1.TypeMeta{
							Kind:       "ConfigurationScanSummary",
							APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
						},
						ObjectMeta: v1.ObjectMeta{
							Name: "wardle",
						},
						Spec: softwarecomposition.ConfigurationScanSummarySpec{
							Severities: softwarecomposition.WorkloadConfigurationScanSeveritiesSummary{
								Critical: 3,
								High:     4,
								Medium:   3,
								Low:      4,
							},
							WorkloadConfigurationScanSummaryIdentifiers: []softwarecomposition.WorkloadConfigurationScanSummaryIdentifier{
								{
									Namespace: "wardle",
									Kind:      "WorkloadConfigurationScanSummary",
									Name:      "workload-3",
								},
								{
									Namespace: "wardle",
									Kind:      "WorkloadConfigurationScanSummary",
									Name:      "workload-4",
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range test {
		got := generateConfigurationScanSummaryForCluster(tt.wlConfigurationScanSummaryList)

		for _, item := range got.Items {
			for _, expectedItem := range tt.expected.Items {
				if item.Name == expectedItem.Name {
					assert.Equal(t, item.APIVersion, expectedItem.APIVersion)

					assert.Equal(t, item.Spec.Severities.Critical, expectedItem.Spec.Severities.Critical)
					assert.Equal(t, item.Spec.Severities.High, expectedItem.Spec.Severities.High)
					assert.Equal(t, item.Spec.Severities.Medium, expectedItem.Spec.Severities.Medium)
					assert.Equal(t, item.Spec.Severities.Low, expectedItem.Spec.Severities.Low)
					assert.Equal(t, item.Spec.Severities.Unknown, expectedItem.Spec.Severities.Unknown)
				}
			}
		}
	}
}
