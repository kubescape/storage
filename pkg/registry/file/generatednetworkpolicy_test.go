package file

import (
	"context"
	"testing"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/stretchr/testify/require"
	"zombiezen.com/go/sqlite/sqlitemigration"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
)

func TestGeneratedNetworkPolicyStorage_Get(t *testing.T) {
	type args struct {
		key    string
		opts   storage.GetOptions
		objPtr runtime.Object
	}
	tests := []struct {
		name           string
		args           args
		create         bool
		noWorkloadName bool
		expectedError  error
		want           runtime.Object
	}{
		{
			name: "no existing objects return empty list",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/generatednetworkpolicies/kubescape/toto",
			},
			expectedError: storage.NewKeyNotFoundError("/spdx.softwarecomposition.kubescape.io/networkneighborhoods/kubescape/toto", 0),
		},
		{
			name: "existing object is returned",
			args: args{
				key:    "/spdx.softwarecomposition.kubescape.io/generatednetworkpolicies/kubescape/toto",
				objPtr: &softwarecomposition.GeneratedNetworkPolicy{},
			},
			expectedError: nil,
			create:        true,
			want: &softwarecomposition.GeneratedNetworkPolicy{
				TypeMeta: v1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
				},
				ObjectMeta: v1.ObjectMeta{
					Name:              "toto",
					Namespace:         "kubescape",
					CreationTimestamp: v1.Time{},
					Labels: map[string]string{
						helpersv1.KindMetadataKey: "Deployment",
						helpersv1.NameMetadataKey: "totowl",
					},
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
						Name:      "deployment-totowl",
						Namespace: "kubescape",
						Labels: map[string]string{
							helpersv1.KindMetadataKey: "Deployment",
							helpersv1.NameMetadataKey: "totowl",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{},
						Egress:  []softwarecomposition.NetworkPolicyEgressRule{},
					},
				},
				PoliciesRef: []softwarecomposition.PolicyRef{},
			},
		},
		{
			name: "missing workload name label",
			args: args{
				key:    "/spdx.softwarecomposition.kubescape.io/generatednetworkpolicies/kubescape/toto",
				objPtr: &softwarecomposition.GeneratedNetworkPolicy{},
			},
			expectedError:  nil,
			create:         true,
			noWorkloadName: true,
			want: &softwarecomposition.GeneratedNetworkPolicy{
				TypeMeta: v1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
				},
				ObjectMeta: v1.ObjectMeta{
					Name:              "toto",
					Namespace:         "kubescape",
					CreationTimestamp: v1.Time{},
					Labels: map[string]string{
						helpersv1.KindMetadataKey: "Deployment",
					},
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
						Name:      "deployment-toto",
						Namespace: "kubescape",
						Labels: map[string]string{
							helpersv1.KindMetadataKey: "Deployment",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{},
						Egress:  []softwarecomposition.NetworkPolicyEgressRule{},
					},
				},
				PoliciesRef: []softwarecomposition.PolicyRef{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := NewTestPool(t.TempDir())
			require.NotNil(t, pool)
			defer func(pool *sqlitemigration.Pool) {
				_ = pool.Close()
			}(pool)
			sch := scheme.Scheme
			require.NoError(t, softwarecomposition.AddToScheme(sch))
			realStorage := NewStorageImpl(afero.NewMemMapFs(), "/", pool, nil, sch)
			generatedNetworkPolicyStorage := NewGeneratedNetworkPolicyStorage(realStorage)

			if tt.create {
				wlObj := &softwarecomposition.NetworkNeighborhood{
					TypeMeta: v1.TypeMeta{
						Kind:       "NetworkNeighborhood",
						APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
					},
					ObjectMeta: v1.ObjectMeta{
						Name:      "toto",
						Namespace: "kubescape",
						Annotations: map[string]string{
							helpersv1.StatusMetadataKey: helpersv1.Ready,
						},
						Labels: map[string]string{
							helpersv1.KindMetadataKey: "Deployment",
							helpersv1.NameMetadataKey: "totowl",
						},
					},
				}
				if tt.noWorkloadName {
					delete(wlObj.ObjectMeta.Labels, helpersv1.NameMetadataKey)
				}
				err := realStorage.Create(context.TODO(), "/spdx.softwarecomposition.kubescape.io/networkneighborhoods/kubescape/toto", wlObj, nil, 0)
				assert.NoError(t, err)
			}

			err := generatedNetworkPolicyStorage.Get(context.TODO(), tt.args.key, tt.args.opts, tt.args.objPtr)

			if tt.expectedError != nil {
				assert.EqualError(t, err, tt.expectedError.Error())
			}
			if tt.args.objPtr != nil {
				tt.args.objPtr.(*softwarecomposition.GeneratedNetworkPolicy).CreationTimestamp = v1.Time{}
			}

			assert.Equal(t, tt.want, tt.args.objPtr)
		})
	}
}

func TestGeneratedNetworkPolicyStorage_Count(t *testing.T) {
	storageImpl := NewStorageImpl(afero.NewMemMapFs(), "", nil, nil, nil)
	generatedNetworkPolicyStorage := NewGeneratedNetworkPolicyStorage(storageImpl)

	count, err := generatedNetworkPolicyStorage.Count("random")

	assert.Equal(t, int64(0), count)

	expectedError := storage.NewInvalidObjError("random", operationNotSupportedMsg)

	assert.EqualError(t, err, expectedError.Error())
}

func TestGeneratedNetworkPolicyStorage_Create(t *testing.T) {
	storageImpl := NewStorageImpl(afero.NewMemMapFs(), "", nil, nil, nil)
	generatedNetworkPolicyStorage := NewGeneratedNetworkPolicyStorage(storageImpl)

	err := generatedNetworkPolicyStorage.Create(context.TODO(), "", nil, nil, 0)

	expectedError := storage.NewInvalidObjError("", operationNotSupportedMsg)

	assert.EqualError(t, err, expectedError.Error())
}

func TestGeneratedNetworkPolicyStorage_Delete(t *testing.T) {
	storageImpl := NewStorageImpl(afero.NewMemMapFs(), "", nil, nil, nil)
	generatedNetworkPolicyStorage := NewGeneratedNetworkPolicyStorage(storageImpl)

	err := generatedNetworkPolicyStorage.Delete(context.TODO(), "", nil, nil, nil, nil)

	expectedError := storage.NewInvalidObjError("", operationNotSupportedMsg)

	assert.EqualError(t, err, expectedError.Error())
}

func TestGeneratedNetworkPolicyStorage_Watch(t *testing.T) {
	storageImpl := NewStorageImpl(afero.NewMemMapFs(), "", nil, nil, nil)
	generatedNetworkPolicyStorage := NewGeneratedNetworkPolicyStorage(storageImpl)

	_, err := generatedNetworkPolicyStorage.Watch(context.TODO(), "", storage.ListOptions{})

	expectedError := storage.NewInvalidObjError("", operationNotSupportedMsg)

	assert.EqualError(t, err, expectedError.Error())
}

func TestGeneratedNetworkPolicyStorage_GuaranteedUpdate(t *testing.T) {
	storageImpl := NewStorageImpl(afero.NewMemMapFs(), "", nil, nil, nil)
	generatedNetworkPolicyStorage := NewGeneratedNetworkPolicyStorage(storageImpl)

	err := generatedNetworkPolicyStorage.GuaranteedUpdate(context.TODO(), "", nil, false, nil, nil, nil)

	expectedError := storage.NewInvalidObjError("", operationNotSupportedMsg)

	assert.EqualError(t, err, expectedError.Error())
}
