package v1beta1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCollapseConfigurationSpec_ProtobufRoundTrip pins every spec field
// through the hand-maintained protobuf marshal/unmarshal code.
func TestCollapseConfigurationSpec_ProtobufRoundTrip(t *testing.T) {
	in := CollapseConfigurationSpec{
		OpenDynamicThreshold:     50,
		EndpointDynamicThreshold: 100,
		CollapseConfigs:          []CollapseConfigEntry{{Prefix: "/usr/bin/bash", Threshold: 200}},
		NetworkIPGroupThreshold:  40,
		NetworkCIDRFloorBits:     24,
		ExecDynamicThreshold:     300,
	}
	data, err := in.Marshal()
	require.NoError(t, err)
	require.Equal(t, in.Size(), len(data))

	var out CollapseConfigurationSpec
	require.NoError(t, out.Unmarshal(data))
	assert.Equal(t, in, out)
}
