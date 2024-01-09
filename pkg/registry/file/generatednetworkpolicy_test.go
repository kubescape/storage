package file

import (
	"context"
	"testing"

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
		name          string
		args          args
		create        bool
		expectedError error
		want          runtime.Object
	}{
		{
			name: "no existing objects return empty list",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/generatednetworkpolicies/kubescape/toto",
			},
			expectedError: storage.NewKeyNotFoundError("/spdx.softwarecomposition.kubescape.io/networkneighborses/kubescape/toto", 0),
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
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				ObjectMeta: v1.ObjectMeta{
					Name:              "toto",
					Namespace:         "kubescape",
					CreationTimestamp: v1.Time{},
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: v1.ObjectMeta{
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
						Name:      "toto",
						Namespace: "kubescape",
					},
				},
				PoliciesRef: []softwarecomposition.PolicyRef{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			realStorage := NewStorageImpl(afero.NewMemMapFs(), "/")
			generatedNetworkPolicyStorage := NewGeneratedNetworkPolicyStorage(&realStorage)

			if tt.create {
				wlObj := &softwarecomposition.NetworkNeighbors{
					TypeMeta: v1.TypeMeta{
						Kind:       "NetworkNeighbors",
						APIVersion: "spdx.softwarecomposition.kubescape.io",
					},
					ObjectMeta: v1.ObjectMeta{
						Name:      "toto",
						Namespace: "kubescape",
					},
				}
				err := realStorage.Create(context.TODO(), "/spdx.softwarecomposition.kubescape.io/networkneighborses/kubescape/toto", wlObj, nil, 0)
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
	storageImpl := NewStorageImpl(afero.NewMemMapFs(), "")
	generatedNetworkPolicyStorage := NewGeneratedNetworkPolicyStorage(&storageImpl)

	count, err := generatedNetworkPolicyStorage.Count("random")

	assert.Equal(t, int64(0), count)

	expectedError := storage.NewInvalidObjError("random", operationNotSupportedMsg)

	assert.EqualError(t, err, expectedError.Error())
}

func TestGeneratedNetworkPolicyStorage_Create(t *testing.T) {
	storageImpl := NewStorageImpl(afero.NewMemMapFs(), "")
	generatedNetworkPolicyStorage := NewGeneratedNetworkPolicyStorage(&storageImpl)

	err := generatedNetworkPolicyStorage.Create(nil, "", nil, nil, 0)

	expectedError := storage.NewInvalidObjError("", operationNotSupportedMsg)

	assert.EqualError(t, err, expectedError.Error())
}

func TestGeneratedNetworkPolicyStorage_Delete(t *testing.T) {
	storageImpl := NewStorageImpl(afero.NewMemMapFs(), "")
	generatedNetworkPolicyStorage := NewGeneratedNetworkPolicyStorage(&storageImpl)

	err := generatedNetworkPolicyStorage.Delete(nil, "", nil, nil, nil, nil)

	expectedError := storage.NewInvalidObjError("", operationNotSupportedMsg)

	assert.EqualError(t, err, expectedError.Error())
}

func TestGeneratedNetworkPolicyStorage_Watch(t *testing.T) {
	storageImpl := NewStorageImpl(afero.NewMemMapFs(), "")
	generatedNetworkPolicyStorage := NewGeneratedNetworkPolicyStorage(&storageImpl)

	_, err := generatedNetworkPolicyStorage.Watch(nil, "", storage.ListOptions{})

	expectedError := storage.NewInvalidObjError("", operationNotSupportedMsg)

	assert.EqualError(t, err, expectedError.Error())
}

func TestGeneratedNetworkPolicyStorage_GuaranteedUpdate(t *testing.T) {
	storageImpl := NewStorageImpl(afero.NewMemMapFs(), "")
	generatedNetworkPolicyStorage := NewGeneratedNetworkPolicyStorage(&storageImpl)

	err := generatedNetworkPolicyStorage.GuaranteedUpdate(nil, "", nil, false, nil, nil, nil)

	expectedError := storage.NewInvalidObjError("", operationNotSupportedMsg)

	assert.EqualError(t, err, expectedError.Error())
}
