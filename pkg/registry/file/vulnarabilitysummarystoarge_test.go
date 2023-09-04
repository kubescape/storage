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
	realStorage := NewStorageImpl(afero.NewMemMapFs(), "/")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewVulnerabilitySummaryStorage(&realStorage)
			err := s.Create(context.TODO(), tt.args.key, tt.args.obj, tt.args.out, tt.args.in4)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, err, storage.NewInvalidObjError(tt.args.key, operationNotSupportedMsg))

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
	realStorage := NewStorageImpl(afero.NewMemMapFs(), "/")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewVulnerabilitySummaryStorage(&realStorage)
			err := s.Delete(context.TODO(), tt.args.key, tt.args.obj, tt.args.precondition, tt.args.validateDeletionFunc, tt.args.cachedObj)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, err, storage.NewInvalidObjError(tt.args.key, operationNotSupportedMsg))

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
	realStorage := NewStorageImpl(afero.NewMemMapFs(), "/")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewVulnerabilitySummaryStorage(&realStorage)
			_, err := s.Watch(context.TODO(), tt.args.key, storage.ListOptions{})
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, err, storage.NewInvalidObjError(tt.args.key, operationNotSupportedMsg))

			}
		})
	}
}

func TestVulnSummaryStorageImpl_GetList(t *testing.T) {
	type args struct {
		keyExpectedObj string
		expectedObj    *softwarecomposition.VulnerabilitySummaryList
		keyCreatedObj  []string
		createdObj     []*softwarecomposition.VulnerabilityManifestSummary
	}
	tests := []struct {
		name      string
		args      args
		createObj bool
		wantErr   bool
	}{
		{
			name: "get - from one created object",
			args: args{
				keyExpectedObj: "/spdx.softwarecomposition.kubescape.io/vulnerabilitysummaries/any",
				expectedObj: &softwarecomposition.VulnerabilitySummaryList{
					TypeMeta: v1.TypeMeta{
						Kind:       "VulnerabilitySummary",
						APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
					},
					Items: []softwarecomposition.VulnerabilitySummary{
						softwarecomposition.VulnerabilitySummary{
							TypeMeta: v1.TypeMeta{
								Kind:       "VulnerabilitySummary",
								APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
							},
							Spec: softwarecomposition.VulnerabilitySummarySpec{
								WorkloadVulnerabilitiesObj: []softwarecomposition.VulnerabilitiesObjScope{
									softwarecomposition.VulnerabilitiesObjScope{
										Kind: "vulnerabilitymanifestsummary",
									},
								},
							},
						},
					},
				},
				keyCreatedObj: []string{"/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifestsummaries/any/any"},
				createdObj:    []*softwarecomposition.VulnerabilityManifestSummary{&softwarecomposition.VulnerabilityManifestSummary{}},
			},
			createObj: true, wantErr: false,
		},
		{
			name: "get - from two created object",
			args: args{
				keyExpectedObj: "/spdx.softwarecomposition.kubescape.io/vulnerabilitysummaries/any",
				expectedObj: &softwarecomposition.VulnerabilitySummaryList{
					TypeMeta: v1.TypeMeta{
						Kind:       "VulnerabilitySummary",
						APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
					},
					Items: []softwarecomposition.VulnerabilitySummary{
						softwarecomposition.VulnerabilitySummary{
							TypeMeta: v1.TypeMeta{
								Kind:       "VulnerabilitySummary",
								APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
							},
							ObjectMeta: v1.ObjectMeta{
								Name: "any",
							},
							Spec: softwarecomposition.VulnerabilitySummarySpec{
								Severities: softwarecomposition.SeveritySummary{
									Negligible: softwarecomposition.VulnerabilityCounters{
										All:      1,
										Relevant: 0,
									},
								},
								WorkloadVulnerabilitiesObj: []softwarecomposition.VulnerabilitiesObjScope{
									softwarecomposition.VulnerabilitiesObjScope{
										Name:      "any",
										Namespace: "any",
										Kind:      "vulnerabilitymanifestsummary",
									},
								},
							},
						},
						softwarecomposition.VulnerabilitySummary{
							TypeMeta: v1.TypeMeta{
								Kind:       "VulnerabilitySummary",
								APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
							},
							ObjectMeta: v1.ObjectMeta{
								Name: "many",
							},
							Spec: softwarecomposition.VulnerabilitySummarySpec{
								Severities: softwarecomposition.SeveritySummary{
									Critical: softwarecomposition.VulnerabilityCounters{
										All:      1,
										Relevant: 0,
									},
								},
								WorkloadVulnerabilitiesObj: []softwarecomposition.VulnerabilitiesObjScope{
									softwarecomposition.VulnerabilitiesObjScope{
										Kind:      "vulnerabilitymanifestsummary",
										Name:      "any",
										Namespace: "many",
									},
								},
							},
						},
					},
				},
				keyCreatedObj: []string{"/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifestsummaries/any/any", "/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifestsummaries/many/any"},
				createdObj: []*softwarecomposition.VulnerabilityManifestSummary{&softwarecomposition.VulnerabilityManifestSummary{
					ObjectMeta: v1.ObjectMeta{
						Name:      "any",
						Namespace: "any",
					},
					Spec: softwarecomposition.VulnerabilityManifestSummarySpec{
						Severities: softwarecomposition.SeveritySummary{
							Negligible: softwarecomposition.VulnerabilityCounters{
								All:      1,
								Relevant: 0,
							},
						},
					},
				}, &softwarecomposition.VulnerabilityManifestSummary{
					ObjectMeta: v1.ObjectMeta{
						Name:      "any",
						Namespace: "many",
					},
					Spec: softwarecomposition.VulnerabilityManifestSummarySpec{
						Severities: softwarecomposition.SeveritySummary{
							Critical: softwarecomposition.VulnerabilityCounters{
								All:      1,
								Relevant: 0,
							},
						},
					},
				}},
			},
			createObj: true, wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			realStorage := NewStorageImpl(afero.NewMemMapFs(), "/")
			if tt.createObj {
				for i := range tt.args.keyCreatedObj {
					err := realStorage.Create(context.TODO(), tt.args.keyCreatedObj[i], tt.args.createdObj[i], nil, 0)
					assert.Equal(t, err, nil)
				}
			}
			s := NewVulnerabilitySummaryStorage(&realStorage)
			o := &softwarecomposition.VulnerabilitySummaryList{}
			err := s.GetList(context.TODO(), tt.args.keyExpectedObj, storage.ListOptions{}, o)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, err, storage.NewKeyNotFoundError(tt.name, 0))
			} else {
				assert.Equal(t, err, nil)
				for i := range o.Items {
					// copy the timestamp since it is created when generated, so it can be known at the test begin
					o.Items[i].CreationTimestamp = tt.args.createdObj[i].CreationTimestamp
				}
				assert.Equal(t, tt.args.expectedObj, o)
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
	realStorage := NewStorageImpl(afero.NewMemMapFs(), "/")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewVulnerabilitySummaryStorage(&realStorage)
			err := s.GuaranteedUpdate(context.TODO(), tt.args.key, tt.args.obj, true, tt.args.preconditions, tt.args.tryUpdate, tt.args.cachedExistingObject)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, err, storage.NewInvalidObjError(tt.args.key, operationNotSupportedMsg))

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
	realStorage := NewStorageImpl(afero.NewMemMapFs(), "/")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewVulnerabilitySummaryStorage(&realStorage)
			_, err := s.Count(tt.args.key)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, err, storage.NewInvalidObjError(tt.args.key, operationNotSupportedMsg))

			}
		})
	}
}

func TestVulnSummaryStorageImpl_Get(t *testing.T) {
	type args struct {
		keyExpectedObj string
		expectedObj    *softwarecomposition.VulnerabilitySummary
		keyCreatedObj  []string
		createdObj     []*softwarecomposition.VulnerabilityManifestSummary
	}
	tests := []struct {
		name      string
		args      args
		createObj bool
		wantErr   bool
	}{
		{
			name: "get - from one created object",
			args: args{
				keyExpectedObj: "/spdx.softwarecomposition.kubescape.io/vulnerabilitysummaries/any",
				expectedObj: &softwarecomposition.VulnerabilitySummary{
					TypeMeta: v1.TypeMeta{
						Kind:       "VulnerabilitySummary",
						APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
					},
					ObjectMeta: v1.ObjectMeta{
						Name: "any",
					},
					Spec: softwarecomposition.VulnerabilitySummarySpec{
						WorkloadVulnerabilitiesObj: []softwarecomposition.VulnerabilitiesObjScope{
							softwarecomposition.VulnerabilitiesObjScope{
								Kind: "vulnerabilitymanifestsummary",
							},
						},
					},
				},
				keyCreatedObj: []string{"/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifestsummaries/any/any"},
				createdObj:    []*softwarecomposition.VulnerabilityManifestSummary{&softwarecomposition.VulnerabilityManifestSummary{}},
			},
			createObj: true, wantErr: false,
		},
		{
			name: "get - from two created object",
			args: args{
				keyExpectedObj: "/spdx.softwarecomposition.kubescape.io/vulnerabilitysummaries/any",
				expectedObj: &softwarecomposition.VulnerabilitySummary{
					TypeMeta: v1.TypeMeta{
						Kind:       "VulnerabilitySummary",
						APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
					},
					ObjectMeta: v1.ObjectMeta{
						Name: "any",
					},
					Spec: softwarecomposition.VulnerabilitySummarySpec{
						WorkloadVulnerabilitiesObj: []softwarecomposition.VulnerabilitiesObjScope{
							softwarecomposition.VulnerabilitiesObjScope{
								Kind: "vulnerabilitymanifestsummary",
							},
							softwarecomposition.VulnerabilitiesObjScope{
								Kind: "vulnerabilitymanifestsummary",
							},
						},
					},
				},
				keyCreatedObj: []string{"/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifestsummaries/any/any", "/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifestsummaries/any/many"},
				createdObj:    []*softwarecomposition.VulnerabilityManifestSummary{&softwarecomposition.VulnerabilityManifestSummary{}, &softwarecomposition.VulnerabilityManifestSummary{}},
			},
			createObj: true, wantErr: false,
		},
	}
	realStorage := NewStorageImpl(afero.NewMemMapFs(), "/")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.createObj {
				for i := range tt.args.keyCreatedObj {
					err := realStorage.Create(context.TODO(), tt.args.keyCreatedObj[i], tt.args.createdObj[i], nil, 0)
					assert.Equal(t, err, nil)
				}
			}
			s := NewVulnerabilitySummaryStorage(&realStorage)
			o := &softwarecomposition.VulnerabilitySummary{}
			err := s.Get(context.TODO(), tt.args.keyExpectedObj, storage.GetOptions{}, o)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, err, storage.NewKeyNotFoundError(tt.name, 0))

			} else {
				assert.Equal(t, err, nil)
				// copy the timestamp since it is created when generated, so it can be known at the test begin
				tt.args.expectedObj.CreationTimestamp = o.CreationTimestamp
				assert.Equal(t, tt.args.expectedObj, o)
			}
		})
	}
}
