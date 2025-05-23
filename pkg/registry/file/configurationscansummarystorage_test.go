package file

import (
	"context"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

func TestConfigurationScanSummaryStorage_Count(t *testing.T) {
	storageImpl := NewStorageImpl(afero.NewMemMapFs(), "", nil, nil, nil)
	configScanSummaryStorage := NewConfigurationScanSummaryStorage(storageImpl)

	count, err := configScanSummaryStorage.Count("random")

	assert.Equal(t, int64(0), count)

	expectedError := storage.NewInvalidObjError("random", operationNotSupportedMsg)

	assert.EqualError(t, err, expectedError.Error())
}

func TestConfigurationScanSummaryStorage_Create(t *testing.T) {
	storageImpl := NewStorageImpl(afero.NewMemMapFs(), "", nil, nil, nil)
	configScanSummaryStorage := NewConfigurationScanSummaryStorage(storageImpl)

	err := configScanSummaryStorage.Create(context.TODO(), "", nil, nil, 0)

	expectedError := storage.NewInvalidObjError("", operationNotSupportedMsg)

	assert.EqualError(t, err, expectedError.Error())
}

func TestConfigurationScanSummaryStorage_Delete(t *testing.T) {
	storageImpl := NewStorageImpl(afero.NewMemMapFs(), "", nil, nil, nil)
	configScanSummaryStorage := NewConfigurationScanSummaryStorage(storageImpl)

	err := configScanSummaryStorage.Delete(context.TODO(), "", nil, nil, nil, nil)

	expectedError := storage.NewInvalidObjError("", operationNotSupportedMsg)

	assert.EqualError(t, err, expectedError.Error())
}

func TestConfigurationScanSummaryStorage_Watch(t *testing.T) {
	storageImpl := NewStorageImpl(afero.NewMemMapFs(), "", nil, nil, nil)
	configScanSummaryStorage := NewConfigurationScanSummaryStorage(storageImpl)

	_, err := configScanSummaryStorage.Watch(context.TODO(), "", storage.ListOptions{})

	expectedError := storage.NewInvalidObjError("", operationNotSupportedMsg)

	assert.EqualError(t, err, expectedError.Error())
}

func TestConfigurationScanSummaryStorage_GuaranteedUpdate(t *testing.T) {
	storageImpl := NewStorageImpl(afero.NewMemMapFs(), "", nil, nil, nil)
	configScanSummaryStorage := NewConfigurationScanSummaryStorage(storageImpl)

	err := configScanSummaryStorage.GuaranteedUpdate(context.TODO(), "", nil, false, nil, nil, nil)

	expectedError := storage.NewInvalidObjError("", operationNotSupportedMsg)

	assert.EqualError(t, err, expectedError.Error())
}

func TestConfigurationScanSummaryStorage_Get(t *testing.T) {
	type args struct {
		key    string
		opts   storage.GetOptions
		objPtr runtime.Object
	}
	tests := []struct {
		name          string
		args          args
		create        bool
		expectedError error
		want          runtime.Object
	}{
		{
			name: "no existing objects return empty list",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/configurationscansummaries/kubescape/toto",
			},
			expectedError: storage.NewKeyNotFoundError("/spdx.softwarecomposition.kubescape.io/configurationscansummaries/kubescape/toto", 0),
		},
		{
			name: "existing object is returned",
			args: args{
				key:    "/spdx.softwarecomposition.kubescape.io/configurationscansummaries/kubescape",
				objPtr: &v1beta1.ConfigurationScanSummary{},
			},
			expectedError: nil,
			create:        true,
			want: &v1beta1.ConfigurationScanSummary{
				TypeMeta: v1.TypeMeta{
					Kind:       "ConfigurationScanSummary",
					APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
				},
			},
		},
	}

	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	defer func(pool *sqlitemigration.Pool) {
		_ = pool.Close()
	}(pool)
	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	realStorage := NewStorageImpl(afero.NewMemMapFs(), "/", pool, nil, sch)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configScanSummaryStorage := NewConfigurationScanSummaryStorage(realStorage)
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()
			if tt.create {
				wlObj := &softwarecomposition.WorkloadConfigurationScanSummary{}
				err := realStorage.Create(ctx, "/spdx.softwarecomposition.kubescape.io/workloadconfigurationscansummaries/kubescape/toto", wlObj, nil, 0)
				require.NoError(t, err)
			}

			err := configScanSummaryStorage.Get(ctx, tt.args.key, tt.args.opts, tt.args.objPtr)

			if tt.expectedError != nil {
				assert.EqualError(t, err, tt.expectedError.Error())
			}

			assert.Equal(t, tt.want, tt.args.objPtr)
		})
	}
}

func TestConfigurationScanSummaryStorage_GetList(t *testing.T) {
	type args struct {
		key    string
		opts   storage.ListOptions
		objPtr runtime.Object
	}
	tests := []struct {
		name          string
		args          args
		create        bool
		expectedError error
		want          runtime.Object
	}{
		{
			name: "no existing objects return empty list",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/configurationscansummaries",
			},
		},
		{
			name: "existing object is returned",
			args: args{
				key:    "/spdx.softwarecomposition.kubescape.io/configurationscansummaries",
				objPtr: &v1beta1.ConfigurationScanSummaryList{},
			},
			expectedError: nil,
			create:        true,
			want: &v1beta1.ConfigurationScanSummaryList{
				TypeMeta: v1.TypeMeta{
					Kind:       "ConfigurationScanSummary",
					APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
				},
				Items: []v1beta1.ConfigurationScanSummary{
					{
						TypeMeta: v1.TypeMeta{
							Kind:       "ConfigurationScanSummary",
							APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
						},
					},
				},
			},
		},
	}

	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	defer func(pool *sqlitemigration.Pool) {
		_ = pool.Close()
	}(pool)
	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	realStorage := NewStorageImpl(afero.NewMemMapFs(), "/", pool, nil, sch)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configScanSummaryStorage := NewConfigurationScanSummaryStorage(realStorage)
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()
			if tt.create {
				wlObj := &softwarecomposition.WorkloadConfigurationScanSummary{}
				err := realStorage.Create(ctx, "/spdx.softwarecomposition.kubescape.io/workloadconfigurationscansummaries/kubescape/toto", wlObj, nil, 0)
				require.NoError(t, err)
			}

			err := configScanSummaryStorage.GetList(ctx, tt.args.key, tt.args.opts, tt.args.objPtr)

			if tt.expectedError != nil {
				assert.EqualError(t, err, tt.expectedError.Error())
			}

			assert.Equal(t, tt.want, tt.args.objPtr)
		})
	}
}

func TestGenerateConfigurationScanSummary(t *testing.T) {
	tests := []struct {
		name                           string
		wlConfigurationScanSummaryList softwarecomposition.WorkloadConfigurationScanSummaryList
		want                           softwarecomposition.ConfigurationScanSummary
	}{
		{
			name:                           "no resources",
			wlConfigurationScanSummaryList: softwarecomposition.WorkloadConfigurationScanSummaryList{},
			want: softwarecomposition.ConfigurationScanSummary{
				TypeMeta: v1.TypeMeta{
					Kind:       "ConfigurationScanSummary",
					APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
				},
				ObjectMeta: v1.ObjectMeta{
					Name: "default",
				},
			},
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
			got := buildConfigurationScanSummary(tt.wlConfigurationScanSummaryList, "default")

			assert.Equal(t, got.APIVersion, tt.want.APIVersion)
			assert.Equal(t, got.Kind, tt.want.Kind)
			assert.Equal(t, got.Name, tt.want.Name)

			assert.Equal(t, got.Spec.Severities.Critical, tt.want.Spec.Severities.Critical)
			assert.Equal(t, got.Spec.Severities.High, tt.want.Spec.Severities.High)
			assert.Equal(t, got.Spec.Severities.Medium, tt.want.Spec.Severities.Medium)
			assert.Equal(t, got.Spec.Severities.Low, tt.want.Spec.Severities.Low)
			assert.Equal(t, got.Spec.Severities.Unknown, tt.want.Spec.Severities.Unknown)

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
		got := buildConfigurationScanSummaryForCluster(tt.wlConfigurationScanSummaryList)

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

func TestGetNamespaceFromKey(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		expected string
	}{
		{
			name:     "namespace1",
			key:      "/spdx.softwarecomposition.kubescape.io/ConfigurationScanSummary/namespace1",
			expected: "namespace1",
		},
		{
			name:     "no namespace",
			key:      "/spdx.softwarecomposition.kubescape.io/ConfigurationScanSummary/",
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := getNamespaceFromKey(test.key)
			if actual != test.expected {
				t.Errorf("Expected %s, got %s", test.expected, actual)
			}
		})
	}

}
