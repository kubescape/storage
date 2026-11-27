package file

import (
	"context"
	"testing"

	"github.com/armosec/armoapi-go/armotypes"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValidateProfileIdentifier(t *testing.T) {
	k8s := func(cluster, name string) armotypes.ProfileIdentifier {
		return armotypes.ProfileIdentifier{ProfileScope: armotypes.ProfileScope{HostType: armotypes.HostTypeKubernetes, Cluster: cluster, Namespace: "ns"}, Name: name}
	}
	ecs := func(cluster, region string) armotypes.ProfileIdentifier {
		return armotypes.ProfileIdentifier{ProfileScope: armotypes.ProfileScope{HostType: armotypes.HostTypeEcsEc2, Cluster: cluster, CloudAccountIdentifier: "123456789012", Region: region}, Name: "task"}
	}
	assert.NoError(t, ValidateProfileIdentifier(k8s("prod", "replicaset-app")))
	assert.NoError(t, ValidateProfileIdentifier(k8s("", "replicaset-app")), "a Kubernetes key keeps its legacy shape without a cluster")
	assert.NoError(t, ValidateProfileIdentifier(ecs("prod", "eu-west-1")))
	for name, id := range map[string]armotypes.ProfileIdentifier{
		"a slash adds a segment":            k8s("prod/extra", "replicaset-app"),
		"dot-dot walks up":                  k8s("..", "replicaset-app"),
		"a percent sign":                    k8s("pr%2fod", "replicaset-app"),
		"a backslash":                       k8s("prod\\extra", "replicaset-app"),
		"an empty ECS cluster shifts shape": ecs("", "eu-west-1"),
		"an empty ECS region shifts shape":  ecs("prod", ""),
	} {
		assert.Error(t, ValidateProfileIdentifier(id), name)
	}
}

// PreSave is the writer-side guard: a profile whose scope annotation cannot be a key
// segment is refused before any key is built from it.
func TestPreSaveRefusesAProfileWhoseScopeIsNotAKeySegment(t *testing.T) {
	a := &ContainerProfileProcessor{HostType: armotypes.HostTypeKubernetes}
	profile := &softwarecomposition.ContainerProfile{ObjectMeta: metav1.ObjectMeta{
		Name: "replicaset-app-1234-5678", Namespace: "ns",
		Annotations: map[string]string{helpers.ClusterMetadataKey: "../../etc"},
	}}
	require.Error(t, a.PreSave(context.Background(), profile))

	row, _, ok := a.TimeSeriesRowFor(&softwarecomposition.ContainerProfile{ObjectMeta: metav1.ObjectMeta{
		Name: "replicaset-app-1234-5678-abcd", Namespace: "ns",
		Annotations: map[string]string{helpers.ReportSeriesIdMetadataKey: "s1", helpers.ClusterMetadataKey: "a/b"},
	}})
	assert.False(t, ok)
	assert.Empty(t, row.Name)
}
