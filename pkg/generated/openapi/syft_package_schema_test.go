package openapi_test

import (
	"encoding/json"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/generated/openapi"
	"github.com/stretchr/testify/require"
	"k8s.io/kube-openapi/pkg/schemaconv"
	"k8s.io/kube-openapi/pkg/validation/spec"
	smdschema "sigs.k8s.io/structured-merge-diff/v6/schema"
	"sigs.k8s.io/structured-merge-diff/v6/typed"
)

// TestSyftPackageManagedFieldsConversion builds managed fields for an SBOM whose
// package has file locations and object-valued metadata. Location, LocationData
// and SyftPackage embed structs that encoding/json flattens, so the schema has
// to declare their fields at the parent level too.
func TestSyftPackageManagedFieldsConversion(t *testing.T) {
	definitions := openapi.GetOpenAPIDefinitions(func(name string) spec.Ref {
		return spec.MustCreateRef("#/definitions/" + name)
	})
	models := make(map[string]*spec.Schema, len(definitions))
	for name, definition := range definitions {
		models[name] = &definition.Schema
	}
	schema, err := schemaconv.ToSchemaFromOpenAPI(models, false)
	require.NoError(t, err)
	parser := typed.Parser{Schema: smdschema.Schema{Types: schema.Types}}

	location := v1beta1.Location{
		LocationData: v1beta1.LocationData{
			Coordinates: v1beta1.Coordinates{RealPath: "/usr/lib/os-release", FileSystemID: "sha256:abc"},
			VirtualPath: "/etc/os-release",
		},
		LocationMetadata: v1beta1.LocationMetadata{Annotations: map[string]string{"evidence": "primary"}},
	}
	sbom := &v1beta1.SBOMSyft{}
	sbom.Spec.Syft.Artifacts = []v1beta1.SyftPackage{{
		PackageBasicData: v1beta1.PackageBasicData{
			ID: "pkg-1", Name: "openssl", Version: "3.0.13", Type: "deb",
			FoundBy: "dpkg-db-cataloger", Locations: []v1beta1.Location{location},
		},
		PackageCustomData: v1beta1.PackageCustomData{
			MetadataType: "dpkg-db-entry",
			Metadata:     json.RawMessage(`{"package":"openssl","files":[{"path":"/usr/bin/openssl"}]}`),
		},
	}}

	converted, err := parser.Type(sbom.OpenAPIModelName()).FromStructured(sbom)
	require.NoError(t, err)
	_, err = converted.ToFieldSet()
	require.NoError(t, err, "managedFields must be able to track an SBOM with located packages")
}
