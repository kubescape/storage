package v1beta1

// Pins the protobuf wire format for the container subtype groups. The
// aggregated apiserver serves protobuf to clients that negotiate it (the
// node-agent storage client sets ContentType=application/vnd.kubernetes.protobuf),
// so a generated.pb.go that predates a field silently drops it on the wire
// while JSON clients still see it - the exact split that made a grouped
// authored document arrive at the node-agent with empty groups.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestContainerProfile_SubtypeGroups_ProtobufRoundTrip(t *testing.T) {
	in := &ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: "default"},
		Spec: ContainerProfileSpec{
			Architectures: []string{"amd64"},
			Containers: []ContainerProfileContainer{
				{Name: "app", Execs: []ExecCalls{{Path: "/usr/bin/id", Args: []string{"/usr/bin/id"}}}},
			},
			InitContainers: []ContainerProfileContainer{
				{Name: "setup", Execs: []ExecCalls{{Path: "/bin/sh", Args: []string{"/bin/sh", "-c", "x"}}}},
			},
			EphemeralContainers: []ContainerProfileContainer{
				{Name: "debug", Execs: []ExecCalls{{Path: "/usr/bin/whoami", Args: []string{"/usr/bin/whoami"}}}},
			},
		},
	}

	data, err := in.Marshal()
	require.NoError(t, err)

	out := &ContainerProfile{}
	require.NoError(t, out.Unmarshal(data))

	require.Len(t, out.Spec.Containers, 1, "containers group must survive the protobuf wire")
	require.Len(t, out.Spec.InitContainers, 1, "initContainers group must survive the protobuf wire")
	require.Len(t, out.Spec.EphemeralContainers, 1, "ephemeralContainers group must survive the protobuf wire")
	assert.Equal(t, "app", out.Spec.Containers[0].Name)
	assert.Equal(t, "/usr/bin/id", out.Spec.Containers[0].Execs[0].Path)
	assert.Equal(t, "setup", out.Spec.InitContainers[0].Name)
	assert.Equal(t, "debug", out.Spec.EphemeralContainers[0].Name)
}
