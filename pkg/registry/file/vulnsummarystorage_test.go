package file

import (
	"context"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
)

func TestVulnSummaryStorageImpl_Create(t *testing.T) {
	type args struct {
		key string
		obj runtime.Object
		out runtime.Object
		in4 uint64
	}
	tests := []struct {
		name     string
		readonly bool
		args     args
		wantErr  bool
		want     runtime.Object
	}{
		{
			name: "not supported",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/vulnerabilitysummaries/kubescape/toto",
				obj: &v1beta1.VulnerabilitySummary{},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := afero.NewReadOnlyFs(afero.NewMemMapFs())
			s := NewVulnerabilitySummaryStorage(fs, DefaultStorageRoot)
			err := s.Create(context.TODO(), tt.args.key, tt.args.obj, tt.args.out, tt.args.in4)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, err, storage.NewMethodNotImplementedError(tt.args.key, ""))
				return
			}
		})
	}
}

func TestVulnSummaryStorageImpl_Delete(t *testing.T) {
	type args struct {
		key                  string
		obj                  runtime.Object
		precondition         *storage.Preconditions
		validateDeletionFunc storage.ValidateObjectFunc
		cachedObj            runtime.Object
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "not supported",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/vulnerabilitysummaries/kubescape/toto",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := afero.NewReadOnlyFs(afero.NewMemMapFs())
			s := NewVulnerabilitySummaryStorage(fs, DefaultStorageRoot)
			err := s.Delete(context.TODO(), tt.args.key, tt.args.obj, tt.args.precondition, tt.args.validateDeletionFunc, tt.args.cachedObj)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, err, storage.NewMethodNotImplementedError(tt.args.key, ""))
				return
			}
		})
	}
}

func TestVulnSummaryStorageImpl_Watch(t *testing.T) {
	type args struct {
		key string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "not supported",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/vulnerabilitysummaries/kubescape/toto",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := afero.NewReadOnlyFs(afero.NewMemMapFs())
			s := NewVulnerabilitySummaryStorage(fs, DefaultStorageRoot)
			_, err := s.Watch(context.TODO(), tt.args.key, storage.ListOptions{})
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, err, storage.NewMethodNotImplementedError(tt.args.key, ""))
				return
			}
		})
	}
}

func TestVulnSummaryStorageImpl_GetList(t *testing.T) {
	type args struct {
		key string
		obj runtime.Object
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "not supported",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/vulnerabilitysummaries/kubescape/toto",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := afero.NewReadOnlyFs(afero.NewMemMapFs())
			s := NewVulnerabilitySummaryStorage(fs, DefaultStorageRoot)
			err := s.GetList(context.TODO(), tt.args.key, storage.ListOptions{}, tt.args.obj)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, err, storage.NewMethodNotImplementedError(tt.args.key, ""))
				return
			}
		})
	}
}

func TestVulnSummaryStorageImpl_GuaranteedUpdate(t *testing.T) {
	type args struct {
		key                  string
		obj                  runtime.Object
		preconditions        *storage.Preconditions
		tryUpdate            storage.UpdateFunc
		cachedExistingObject runtime.Object
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "not supported",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/vulnerabilitysummaries/kubescape/toto",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := afero.NewReadOnlyFs(afero.NewMemMapFs())
			s := NewVulnerabilitySummaryStorage(fs, DefaultStorageRoot)
			err := s.GuaranteedUpdate(context.TODO(), tt.args.key, tt.args.obj, true, tt.args.preconditions, tt.args.tryUpdate, tt.args.cachedExistingObject)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, err, storage.NewMethodNotImplementedError(tt.args.key, ""))
				return
			}
		})
	}
}

func TestVulnSummaryStorageImpl_Count(t *testing.T) {
	type args struct {
		key string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "not supported",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/vulnerabilitysummaries/kubescape/toto",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := afero.NewReadOnlyFs(afero.NewMemMapFs())
			s := NewVulnerabilitySummaryStorage(fs, DefaultStorageRoot)
			_, err := s.Count(tt.args.key)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, err, storage.NewMethodNotImplementedError(tt.args.key, ""))
				return
			}
		})
	}
}

func Test_initVulnSummary(t *testing.T) {
	type args struct {
		scope string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "namespace",
			args: args{
				scope: "/spdx.softwarecomposition.kubescape.io/vulnerabilitysummaries/kubescape/toto",
			},
		},
		{
			name: "cluster",
			args: args{
				scope: "/spdx.softwarecomposition.kubescape.io/vulnerabilitysummaries/kubescape/cluster",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := initVulnSummary(tt.args.scope)
			if tt.name != "cluster" {
				assert.Equal(t, res.Labels["kubescape.io/workload-namespace"], tt.args.scope)
				assert.Equal(t, res.Namespace, tt.args.scope)
			}
			assert.Equal(t, res.Name, tt.args.scope)
		})
	}
}

func Test_summarizeVulnerabilities(t *testing.T) {
	tests := []struct {
		name                 string
		scope                string
		vulnManifestSumm     []softwarecomposition.VulnerabilityManifestSummary
		expectedFullVulnSumm *softwarecomposition.VulnerabilitySummary
	}{
		{
			name:  "test1",
			scope: "aaa",
			vulnManifestSumm: []softwarecomposition.VulnerabilityManifestSummary{
				softwarecomposition.VulnerabilityManifestSummary{
					Spec: softwarecomposition.VulnerabilityManifestSummarySpec{
						Severities: softwarecomposition.SeveritySummary{
							Critical: softwarecomposition.VulnerabilityCounters{
								All:      20,
								Relevant: 6,
							},
							High: softwarecomposition.VulnerabilityCounters{
								All:      20,
								Relevant: 6,
							},
							Medium: softwarecomposition.VulnerabilityCounters{
								All:      20,
								Relevant: 6,
							},
							Low: softwarecomposition.VulnerabilityCounters{
								All:      20,
								Relevant: 6,
							},
							Negligible: softwarecomposition.VulnerabilityCounters{
								All:      20,
								Relevant: 6,
							},
							Unknown: softwarecomposition.VulnerabilityCounters{
								All:      20,
								Relevant: 6,
							},
						},
						Vulnerabilities: softwarecomposition.VulnerabilitiesComponents{
							ImageVulnerabilitiesObj: softwarecomposition.VulnerabilitiesObjScope{
								Name:      "aaa",
								Namespace: "bbb",
								Kind:      "any",
							},
							WorkloadVulnerabilitiesObj: softwarecomposition.VulnerabilitiesObjScope{
								Name:      "ccc",
								Namespace: "ddd",
								Kind:      "many",
							},
						},
					},
				},
			},
			expectedFullVulnSumm: &softwarecomposition.VulnerabilitySummary{
				TypeMeta: v1.TypeMeta{
					APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
					Kind:       "VulnerabilitySummary",
				},
				ObjectMeta: v1.ObjectMeta{
					Name:        "aaa",
					Namespace:   "aaa",
					Labels:      map[string]string{"kubescape.io/workload-namespace": "aaa"},
					Annotations: map[string]string{"kubescape.io/status": ""},
				},
				Spec: softwarecomposition.VulnerabilitySummarySpec{
					Severities: softwarecomposition.SeveritySummary{
						Critical: softwarecomposition.VulnerabilityCounters{
							All:      20,
							Relevant: 6,
						},
						High: softwarecomposition.VulnerabilityCounters{
							All:      20,
							Relevant: 6,
						},
						Medium: softwarecomposition.VulnerabilityCounters{
							All:      20,
							Relevant: 6,
						},
						Low: softwarecomposition.VulnerabilityCounters{
							All:      20,
							Relevant: 6,
						},
						Negligible: softwarecomposition.VulnerabilityCounters{
							All:      20,
							Relevant: 6,
						},
						Unknown: softwarecomposition.VulnerabilityCounters{
							All:      20,
							Relevant: 6,
						},
					},
					WorkloadVulnerabilitiesObj: []softwarecomposition.VulnerabilitiesObjScope{
						softwarecomposition.VulnerabilitiesObjScope{
							Name:      "aaa",
							Namespace: "bbb",
							Kind:      "any",
						},
						softwarecomposition.VulnerabilitiesObjScope{
							Name:      "ccc",
							Namespace: "ddd",
							Kind:      "many",
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := afero.NewReadOnlyFs(afero.NewMemMapFs())
			s := &VulnerabilitySummaryStorage{
				realStore: StorageImpl{
					appFs:           fs,
					watchDispatcher: newWatchDispatcher(),
					root:            "/data",
					versioner:       storage.APIObjectVersioner{},
				},
				versioner: storage.APIObjectVersioner{},
			}
			res := s.summarizeVulnerabilities(context.TODO(), tt.vulnManifestSumm, tt.scope)
			assert.Equal(t, tt.expectedFullVulnSumm.Spec, res.Spec)
			assert.Equal(t, tt.expectedFullVulnSumm.APIVersion, res.APIVersion)
			assert.Equal(t, tt.expectedFullVulnSumm.Kind, res.Kind)
			assert.Equal(t, tt.expectedFullVulnSumm.Labels, res.Labels)
			assert.Equal(t, tt.expectedFullVulnSumm.Annotations, res.Annotations)
			assert.Equal(t, tt.expectedFullVulnSumm.Name, res.Name)
			assert.Equal(t, tt.expectedFullVulnSumm.Namespace, res.Namespace)
		})
	}
}

func TestVulnSummaryStorageImpl_Get(t *testing.T) {
	type args struct {
		key string
		obj *softwarecomposition.VulnerabilitySummary
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "namespace",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/vulnerabilitysummaries/kubescape/toto",
				obj: &softwarecomposition.VulnerabilitySummary{},
			},
			wantErr: false,
		},
		{
			name: "cluster",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/vulnerabilitysummaries/cluster",
				obj: &softwarecomposition.VulnerabilitySummary{},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := afero.NewReadOnlyFs(afero.NewMemMapFs())
			s := NewVulnerabilitySummaryStorage(fs, DefaultStorageRoot)
			o := tt.args.obj.DeepCopyObject()
			err := s.Get(context.TODO(), tt.args.key, storage.GetOptions{}, o)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, err, storage.NewMethodNotImplementedError(tt.name, ""))
				return
			} else {
				assert.Error(t, err, nil)
			}
		})
	}
}
