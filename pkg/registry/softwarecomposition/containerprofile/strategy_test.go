package containerprofile

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

// These tests pin the ContainerProfile registry strategy. The strategy files in
// this package were seeded from the SbomSyft strategy, so the risk is a
// copy-paste that leaves the type mapping pointing at SBOMSyft: GetAttrs would
// then reject every ContainerProfile and the selectable fields would never be
// exposed. The GetAttrs/SelectableFields assertions below fail loudly on that
// mistake.

func newContainerProfile() *softwarecomposition.ContainerProfile {
	return &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "replicaset-nginx-abc123-nginx-1a2b-3c4d",
			Namespace: "kubescape",
			Labels: map[string]string{
				"kubescape.io/workload-kind":           "Deployment",
				"kubescape.io/workload-name":           "nginx",
				"kubescape.io/workload-container-name": "nginx",
			},
		},
	}
}

// TestGetAttrs_ContainerProfile asserts the attribute function accepts a
// ContainerProfile (not an SBOMSyft) and exposes the object's namespace, name,
// and workload labels for selection.
func TestGetAttrs_ContainerProfile(t *testing.T) {
	cp := newContainerProfile()

	labelSet, fieldSet, err := GetAttrs(cp)
	require.NoError(t, err, "GetAttrs must accept a ContainerProfile - a copy-paste to *SBOMSyft would error here")

	// Workload labels must be exposed verbatim so label selectors can target a
	// workload/container.
	assert.Equal(t, "Deployment", labelSet["kubescape.io/workload-kind"])
	assert.Equal(t, "nginx", labelSet["kubescape.io/workload-name"])
	assert.Equal(t, "nginx", labelSet["kubescape.io/workload-container-name"])

	// Namespace/name must be exposed as selectable fields.
	assert.Equal(t, "kubescape", fieldSet["metadata.namespace"], "namespace must be a selectable field")
	assert.Equal(t, "replicaset-nginx-abc123-nginx-1a2b-3c4d", fieldSet["metadata.name"], "name must be a selectable field")
}

// TestGetAttrs_WrongType asserts GetAttrs rejects a non-ContainerProfile object.
func TestGetAttrs_WrongType(t *testing.T) {
	_, _, err := GetAttrs(&softwarecomposition.SBOMSyft{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ContainerProfile")
}

// TestSelectableFields asserts SelectableFields maps to the ContainerProfile's
// own metadata.name/metadata.namespace, not some other object's.
func TestSelectableFields(t *testing.T) {
	cp := newContainerProfile()
	fs := SelectableFields(cp)
	assert.Equal(t, cp.Namespace, fs["metadata.namespace"])
	assert.Equal(t, cp.Name, fs["metadata.name"])
}

// TestMatchWorkloadConfigurationScan wires GetAttrs into a SelectionPredicate and
// asserts a label+field selector matches the profile it describes.
func TestMatchWorkloadConfigurationScan(t *testing.T) {
	cp := newContainerProfile()
	pred := MatchWorkloadConfigurationScan(nil, nil)
	require.NotNil(t, pred.GetAttrs)

	gotLabels, gotFields, err := pred.GetAttrs(cp)
	require.NoError(t, err)
	assert.Equal(t, "nginx", gotLabels["kubescape.io/workload-name"])
	assert.Equal(t, "kubescape", gotFields["metadata.namespace"])
}

func TestNamespaceScoped(t *testing.T) {
	assert.True(t, NewStrategy(nil).NamespaceScoped(), "ContainerProfiles are namespace-scoped")
}

// TestStrategy_CreateUpdateValidate pins the create/update/validate behaviour for
// a ContainerProfile: the strategy does not reject valid profiles and does not
// mutate them on prepare.
func TestStrategy_CreateUpdateValidate(t *testing.T) {
	s := NewStrategy(nil)
	ctx := context.TODO()

	cp := newContainerProfile()
	before := cp.DeepCopy()

	// PrepareForCreate must not mutate the object.
	s.PrepareForCreate(ctx, cp)
	assert.Equal(t, before, cp, "PrepareForCreate must not mutate the ContainerProfile")

	// Validate must accept a valid ContainerProfile.
	assert.Empty(t, s.Validate(ctx, cp), "Validate must not reject a valid ContainerProfile")

	// PrepareForUpdate must not mutate the object.
	updated := newContainerProfile()
	updated.Labels["extra"] = "v"
	updatedBefore := updated.DeepCopy()
	s.PrepareForUpdate(ctx, updated, cp)
	assert.Equal(t, updatedBefore, updated, "PrepareForUpdate must not mutate the ContainerProfile")

	assert.Empty(t, s.ValidateUpdate(ctx, updated, cp))
	assert.Nil(t, s.WarningsOnCreate(ctx, cp))
	assert.Nil(t, s.WarningsOnUpdate(ctx, updated, cp))
	assert.False(t, s.AllowCreateOnUpdate())
	assert.False(t, s.AllowUnconditionalUpdate())
}

// compile-time guard: the strategy implements the interfaces the REST store needs.
var _ interface {
	NamespaceScoped() bool
	PrepareForCreate(context.Context, runtime.Object)
} = SbomSyftStrategy{}
