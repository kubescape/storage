package file

import (
	"context"
	"testing"
	"time"

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
			generatedNetworkPolicyStorage := NewGeneratedNetworkPolicyStorage(realStorage, realStorage)
			ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
			defer cancel()
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
							helpersv1.StatusMetadataKey: helpersv1.Learning,
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
				err := realStorage.Create(ctx, "/spdx.softwarecomposition.kubescape.io/networkneighborhoods/kubescape/toto", wlObj, nil, 0)
				require.NoError(t, err)
			}

			err := generatedNetworkPolicyStorage.Get(ctx, tt.args.key, tt.args.opts, tt.args.objPtr)

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
	generatedNetworkPolicyStorage := NewGeneratedNetworkPolicyStorage(storageImpl, storageImpl)

	count, err := generatedNetworkPolicyStorage.Count("random")

	assert.Equal(t, int64(0), count)

	expectedError := storage.NewInvalidObjError("random", operationNotSupportedMsg)

	assert.EqualError(t, err, expectedError.Error())
}

func TestGeneratedNetworkPolicyStorage_Create(t *testing.T) {
	storageImpl := NewStorageImpl(afero.NewMemMapFs(), "", nil, nil, nil)
	generatedNetworkPolicyStorage := NewGeneratedNetworkPolicyStorage(storageImpl, storageImpl)

	err := generatedNetworkPolicyStorage.Create(context.TODO(), "", nil, nil, 0)

	expectedError := storage.NewInvalidObjError("", operationNotSupportedMsg)

	assert.EqualError(t, err, expectedError.Error())
}

func TestGeneratedNetworkPolicyStorage_Delete(t *testing.T) {
	storageImpl := NewStorageImpl(afero.NewMemMapFs(), "", nil, nil, nil)
	generatedNetworkPolicyStorage := NewGeneratedNetworkPolicyStorage(storageImpl, storageImpl)

	err := generatedNetworkPolicyStorage.Delete(context.TODO(), "", nil, nil, nil, nil, storage.DeleteOptions{})

	expectedError := storage.NewInvalidObjError("", operationNotSupportedMsg)

	assert.EqualError(t, err, expectedError.Error())
}

func TestGeneratedNetworkPolicyStorage_Watch(t *testing.T) {
	storageImpl := NewStorageImpl(afero.NewMemMapFs(), "", nil, nil, nil)
	generatedNetworkPolicyStorage := NewGeneratedNetworkPolicyStorage(storageImpl, storageImpl)

	_, err := generatedNetworkPolicyStorage.Watch(context.TODO(), "", storage.ListOptions{})
	assert.NoError(t, err)
}

func TestGeneratedNetworkPolicyStorage_GuaranteedUpdate(t *testing.T) {
	storageImpl := NewStorageImpl(afero.NewMemMapFs(), "", nil, nil, nil)
	generatedNetworkPolicyStorage := NewGeneratedNetworkPolicyStorage(storageImpl, storageImpl)

	err := generatedNetworkPolicyStorage.GuaranteedUpdate(context.TODO(), "", nil, false, nil, nil, nil)

	expectedError := storage.NewInvalidObjError("", operationNotSupportedMsg)

	assert.EqualError(t, err, expectedError.Error())
}

func TestGeneratedNetworkPolicyStorage_DuplicateEgressRules(t *testing.T) {
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	defer func(pool *sqlitemigration.Pool) {
		_ = pool.Close()
	}(pool)
	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	realStorage := NewStorageImpl(afero.NewMemMapFs(), "/", pool, nil, sch)
	generatedNetworkPolicyStorage := NewGeneratedNetworkPolicyStorage(realStorage, realStorage)
	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()

	// Create a NetworkNeighborhood with duplicate egress rules (simulating the real bug)
	nn := &softwarecomposition.NetworkNeighborhood{
		TypeMeta: v1.TypeMeta{
			Kind:       "NetworkNeighborhood",
			APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      "duplicate-test",
			Namespace: "kubescape",
			Annotations: map[string]string{
				helpersv1.StatusMetadataKey: helpersv1.Learning,
			},
			Labels: map[string]string{
				helpersv1.KindMetadataKey: "Deployment",
				helpersv1.NameMetadataKey: "duplicate-test",
			},
		},
		Spec: softwarecomposition.NetworkNeighborhoodSpec{
			LabelSelector: v1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "duplicate-test-app",
				},
			},
			Containers: []softwarecomposition.NetworkNeighborhoodContainer{
				{
					Egress: []softwarecomposition.NetworkNeighbor{
						// First rule
						{
							Identifier: "redis-1",
							PodSelector: &v1.LabelSelector{
								MatchLabels: map[string]string{
									"app.kubernetes.io/component": "master",
									"app.kubernetes.io/name":      "redis",
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     func() *int32 { p := int32(6379); return &p }(),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-6379",
								},
							},
						},
						// DUPLICATE rule (this simulates the bug)
						{
							Identifier: "redis-1", // Same identifier = duplicate
							PodSelector: &v1.LabelSelector{
								MatchLabels: map[string]string{
									"app.kubernetes.io/component": "master",
									"app.kubernetes.io/name":      "redis",
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     func() *int32 { p := int32(6379); return &p }(),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-6379",
								},
							},
						},
					},
				},
			},
		},
	}

	
	err := realStorage.Create(ctx, "/spdx.softwarecomposition.kubescape.io/networkneighborhoods/kubescape/duplicate-test", nn, nil, 0)
	require.NoError(t, err)

	
	gnp := &softwarecomposition.GeneratedNetworkPolicy{}
	err = generatedNetworkPolicyStorage.Get(ctx, "/spdx.softwarecomposition.kubescape.io/generatednetworkpolicies/kubescape/duplicate-test", storage.GetOptions{}, gnp)
	require.NoError(t, err)

	egressRules := gnp.Spec.Spec.Egress
	t.Logf("Number of egress rules generated: %d", len(egressRules))

	for i, rule := range egressRules {
		t.Logf("Rule %d: Ports=%v, To=%v", i, rule.Ports, rule.To)
	}


	assert.Equal(t, 1, len(egressRules), "Expected 1 egress rule after deduplication, got %d", len(egressRules))

	if len(egressRules) > 1 {
		t.Error("BUG: Duplicate egress rules found!")
	} else {
		t.Log("SUCCESS: No duplicate egress rules!")
	}
}
