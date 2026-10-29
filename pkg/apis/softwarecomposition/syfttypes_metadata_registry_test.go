package softwarecomposition

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func syftPackageJSON(metadataType, metadata string) []byte {
	return []byte(`{
		"id": "pkg-1",
		"name": "example",
		"version": "1.0.0",
		"type": "deb",
		"metadataType": "` + metadataType + `",
		"metadata": ` + metadata + `
	}`)
}

// A name the registry knows decodes into the syft struct and re-encodes with
// that struct's field names.
func TestSyftPackageUnmarshal_KnownMetadataTypeDecodesTyped(t *testing.T) {
	var p SyftPackage
	require.NoError(t, json.Unmarshal(syftPackageJSON("python-uv-lock-entry", `{"index":"https://pypi.org/simple"}`), &p))

	assert.Equal(t, "python-uv-lock-entry", p.MetadataType)

	var got map[string]any
	require.NoError(t, json.Unmarshal(p.Metadata, &got))
	assert.Equal(t, "https://pypi.org/simple", got["index"])
}

// A legacy name still decodes, to the type it has always mapped to.
func TestSyftPackageUnmarshal_LegacyMetadataTypeStillDecodes(t *testing.T) {
	var p SyftPackage
	require.NoError(t, json.Unmarshal(syftPackageJSON("ApkMetadata", `{"package":"busybox","originPackage":"busybox"}`), &p))

	assert.Equal(t, "ApkMetadata", p.MetadataType)

	var got map[string]any
	require.NoError(t, json.Unmarshal(p.Metadata, &got))
	assert.Equal(t, "busybox", got["package"])
}

// This is the property that makes a stale registry lossy-but-safe rather than
// fatal: an unrecognised metadataType must not fail the decode, and the
// metadata must survive as raw JSON.
func TestSyftPackageUnmarshal_UnknownMetadataTypeDegradesToRawJSON(t *testing.T) {
	var p SyftPackage
	err := json.Unmarshal(syftPackageJSON("some-future-syft-entry", `{"someField":"someValue","nested":{"n":1}}`), &p)
	require.NoError(t, err, "an unknown metadataType must not fail the decode")

	assert.Equal(t, "example", p.Name)
	assert.Equal(t, "some-future-syft-entry", p.MetadataType)

	var got map[string]any
	require.NoError(t, json.Unmarshal(p.Metadata, &got))
	assert.Equal(t, "someValue", got["someField"])
	assert.Equal(t, map[string]any{"n": float64(1)}, got["nested"])
}

func TestSyftPackageUnmarshal_NoMetadataType(t *testing.T) {
	var p SyftPackage
	require.NoError(t, json.Unmarshal([]byte(`{"id":"pkg-1","name":"example","version":"1.0.0","type":"deb"}`), &p))
	assert.Equal(t, "example", p.Name)
	assert.Empty(t, p.MetadataType)
}
