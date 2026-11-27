package v1beta1

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSBOMStatusProtobufRoundTrip(t *testing.T) {
	expected := SBOMSyftStatus{Status: "completed", StatusReason: "scan done", Limit: "maxImageSize"}
	t.Run("sbom", func(t *testing.T) {
		before := SBOMSyft{Status: expected}
		wire, err := before.Marshal()
		require.NoError(t, err)
		require.Len(t, wire, before.Size())
		var after SBOMSyft
		require.NoError(t, after.Unmarshal(wire))
		require.Equal(t, expected, after.Status)
	})
	t.Run("filtered", func(t *testing.T) {
		before := SBOMSyftFiltered{Status: expected}
		wire, err := before.Marshal()
		require.NoError(t, err)
		require.Len(t, wire, before.Size())
		var after SBOMSyftFiltered
		require.NoError(t, after.Unmarshal(wire))
		require.Equal(t, expected, after.Status)
	})
}
