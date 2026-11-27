package packagemetadata

import (
	"reflect"
	"testing"

	"github.com/anchore/syft/syft/pkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This list is a hand-maintained copy of syft's internal/packagemetadata/names.go
// (it lives under internal/, so it cannot be imported). These tests pin the
// properties a syft bump must not break.

func TestReflectTypeFromJSONName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  any
	}{
		// current names
		{"current name", "apk-db-entry", pkg.ApkDBEntry{}},
		{"lookup is case-insensitive", "APK-DB-Entry", pkg.ApkDBEntry{}},

		// legacy aliases: documents written by older syft versions still decode
		{"legacy alias", "ApkMetadata", pkg.ApkDBEntry{}},
		{"legacy alias", "AlpmMetadata", pkg.AlpmDBEntry{}},
		{"legacy alias with MetadataType suffix", "ConanLockMetadataType", pkg.ConanV1LockEntry{}},
		// expandLegacyNameVariants derives the "...Metadata" form from "...MetadataType"
		{"derived legacy variant", "ConanLockMetadata", pkg.ConanV1LockEntry{}},
		// ...and the "...MetadataType" form from "...Metadata"
		{"derived legacy variant", "AlpmMetadataType", pkg.AlpmDBEntry{}},

		// types added by the v1.55.0 sync
		{"new in v1.55", "python-uv-lock-entry", pkg.PythonUvLockEntry{}},
		{"new in v1.55", "javascript-pnpm-lock-entry", pkg.PnpmLockEntry{}},
		{"new in v1.55", "gguf-file-header", pkg.GGUFFileHeader{}},
		{"new in v1.55", "conda-metadata-entry", pkg.CondaMetaPackage{}},
		{"new in v1.55, legacy alias", "CondaPackageMetadata", pkg.CondaMetaPackage{}},

		// unknown names resolve to nil, which is what lets the caller keep the
		// metadata as raw JSON instead of failing the decode
		{"unknown name", "not-a-real-metadata-type", nil},
		{"empty name", "", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/"+tt.input, func(t *testing.T) {
			got := ReflectTypeFromJSONName(tt.input)
			if tt.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got, "expected %s to resolve", tt.input)
			assert.Equal(t, reflect.TypeOf(tt.want), got)
		})
	}
}

// Three legacy names are shared by two types each. jsonNamesWithoutLookup
// registers the non-preferred type so it still gets a name when encoding, but
// deliberately keeps it out of the name->type lookup, so the legacy name has to
// keep resolving to the preferred type.
func TestSplitLegacyNamesResolveToThePreferredType(t *testing.T) {
	tests := []struct {
		legacyName string
		want       any
		notWant    any
	}{
		{"HackageMetadataType", pkg.HackageStackYamlLockEntry{}, pkg.HackageStackYamlEntry{}},
		{"PhpComposerJsonMetadata", pkg.PhpComposerLockEntry{}, pkg.PhpComposerInstalledEntry{}},
		{"RpmMetadata", pkg.RpmDBEntry{}, pkg.RpmArchive{}},
		{"RustCargoPackageMetadata", pkg.RustCargoLockEntry{}, pkg.RustBinaryAuditEntry{}},
	}

	for _, tt := range tests {
		t.Run(tt.legacyName, func(t *testing.T) {
			got := ReflectTypeFromJSONName(tt.legacyName)
			require.NotNil(t, got)
			assert.Equal(t, reflect.TypeOf(tt.want), got)
			assert.NotEqual(t, reflect.TypeOf(tt.notWant), got)
		})
	}

	// the non-preferred types are still reachable by their own current names
	assert.Equal(t, reflect.TypeOf(pkg.HackageStackYamlEntry{}), ReflectTypeFromJSONName("haskell-hackage-stack-entry"))
	assert.Equal(t, reflect.TypeOf(pkg.PhpComposerInstalledEntry{}), ReflectTypeFromJSONName("php-composer-installed-entry"))
	assert.Equal(t, reflect.TypeOf(pkg.RpmArchive{}), ReflectTypeFromJSONName("rpm-archive"))
	assert.Equal(t, reflect.TypeOf(pkg.RustBinaryAuditEntry{}), ReflectTypeFromJSONName("rust-cargo-audit-entry"))
}

// Every registered name must resolve, and no two types may claim the same one.
func TestRegistryIsSelfConsistent(t *testing.T) {
	assert.Len(t, jsonTypes.typeToName, 71, "type count changed - re-sync with syft's internal/packagemetadata/names.go")

	for name, typ := range jsonTypes.nameToType {
		assert.NotNil(t, ReflectTypeFromJSONName(name), "registered name %q does not resolve", name)
		assert.Equal(t, typ, ReflectTypeFromJSONName(name))
	}

	for typ, name := range jsonTypes.typeToName {
		assert.NotEmpty(t, name, "type %s has an empty JSON name", typ)
	}
}
