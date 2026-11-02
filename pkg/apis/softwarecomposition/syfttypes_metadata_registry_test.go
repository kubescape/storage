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

// A name the registry knows is decoded into the syft struct and re-encoded from
// it. Asserting a known field survives is not enough to prove that — the raw
// JSON fallback would preserve it too. What separates the two paths is that a
// typed decode drops fields the struct does not declare, and materialises the
// ones it does.
func TestSyftPackageUnmarshal_KnownMetadataTypeDecodesTyped(t *testing.T) {
	var p SyftPackage
	raw := `{"index":"https://pypi.org/simple","notAFieldOfThisType":"dropped"}`
	require.NoError(t, json.Unmarshal(syftPackageJSON("python-uv-lock-entry", raw), &p))

	assert.Equal(t, "python-uv-lock-entry", p.MetadataType)

	var got map[string]any
	require.NoError(t, json.Unmarshal(p.Metadata, &got))
	assert.Equal(t, "https://pypi.org/simple", got["index"])
	assert.NotContains(t, got, "notAFieldOfThisType", "went through the raw-JSON fallback, not pkg.PythonUvLockEntry")
	assert.Contains(t, got, "dependencies", "pkg.PythonUvLockEntry declares dependencies without omitempty")
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

// The two registries do NOT fail the same way, and the difference matters when
// deciding how urgent a sync is. unpackSrcMetadata returns a plain error for an
// unknown non-empty source type, with no errUnknownMetadataType sentinel for
// the caller to swallow, so a missing SOURCE name fails the whole decode —
// unlike a missing PACKAGE metadata name, which degrades to raw JSON.
func TestSyftSourceUnmarshal_UnknownTypeIsAHardError(t *testing.T) {
	var s SyftSource
	err := json.Unmarshal([]byte(`{"id":"src-1","name":"example","type":"some-future-source","metadata":{"a":1}}`), &s)

	require.Error(t, err, "an unknown source type must not silently degrade")
	assert.Contains(t, err.Error(), "unable to find source metadata type")
	assert.Empty(t, s.Metadata, "the metadata is not retained on this path")
}

func TestSyftSourceUnmarshal_KnownTypesDecode(t *testing.T) {
	for _, ty := range []string{"image", "directory", "file", "snap", "oci-model"} {
		t.Run(ty, func(t *testing.T) {
			var s SyftSource
			raw := `{"id":"src-1","name":"example","type":"` + ty + `","metadata":{}}`
			require.NoError(t, json.Unmarshal([]byte(raw), &s))
			assert.Equal(t, ty, s.Type)
		})
	}
}

// An empty type is the documented exception: objects from the watcher arrive
// without one, and that must stay non-fatal.
func TestSyftSourceUnmarshal_EmptyTypeIsNotAnError(t *testing.T) {
	var s SyftSource
	require.NoError(t, json.Unmarshal([]byte(`{"id":"src-1","name":"example"}`), &s))
	assert.Empty(t, s.Type)
}
