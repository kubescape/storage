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

// Exercise the generated schema through the same conversion used by managedFields.
// RawMessage must retain arbitrary JSON, including nested arrays and scalars.
func TestScannerDocumentsManagedFieldsConversion(t *testing.T) {
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

	for _, payload := range []string{`{"nested":{"values":["nginx",42,true,null]}}`, `["nginx",{"version":"1.27.3"}]`, `"legacy-target"`, `42`, `true`, `null`} {
		t.Run(payload, func(t *testing.T) {
			raw := json.RawMessage(payload)
			sbom := &v1beta1.SBOMSyft{}
			sbom.Spec.Syft.ArtifactRelationships = []v1beta1.SyftRelationship{{Parent: "package", Child: "file", Type: "contains", Metadata: raw}}
			sbom.Spec.Syft.SyftSource.Metadata = raw
			sbom.Spec.Syft.SyftDescriptor.Configuration = raw
			manifest := &v1beta1.VulnerabilityManifest{}
			manifest.Spec.Payload.Descriptor_ = v1beta1.Descriptor{Configuration: raw, VulnerabilityDBStatus: raw}
			manifest.Spec.Payload.Source = &v1beta1.Source{Type: "image", Target: raw}
			manifest.Spec.Payload.Matches = []v1beta1.Match{{
				MatchDetails:  []v1beta1.MatchDetails{{SearchedBy: raw, Found: raw}},
				Artifact:      v1beta1.GrypePackage{Metadata: raw},
				Vulnerability: v1beta1.Vulnerability{VulnerabilityMetadata: v1beta1.VulnerabilityMetadata{Cvss: []v1beta1.Cvss{{VendorMetadata: raw}}}},
			}}

			for model, properties := range map[string][]string{
				"Cvss": {"vendorMetadata"}, "Descriptor": {"configuration", "db"},
				"GrypePackage": {"metadata"}, "MatchDetails": {"searchedBy", "found"},
				"PackageCustomData": {"metadata"}, "Source": {"target"},
				"SyftDescriptor": {"configuration"}, "SyftRelationship": {"metadata"},
				"SyftSource": {"metadata"},
			} {
				t.Run(model, func(t *testing.T) {
					var value interface{}
					require.NoError(t, json.Unmarshal(raw, &value))
					object := make(map[string]interface{}, len(properties))
					for _, property := range properties {
						object[property] = value
					}
					converted, err := parser.Type("com.github.kubescape.storage.pkg.apis.softwarecomposition.v1beta1." + model).FromUnstructured(object)
					require.NoError(t, err)
					require.Equal(t, object, converted.AsValue().Unstructured(), "opaque JSON must survive conversion")
				})
			}

			for _, document := range []interface{ OpenAPIModelName() string }{sbom, manifest} {
				t.Run(document.OpenAPIModelName(), func(t *testing.T) {
					converted, err := parser.Type(document.OpenAPIModelName()).FromStructured(document)
					require.NoError(t, err)
					_, err = converted.ToFieldSet()
					require.NoError(t, err, "managedFields must be able to track the document")
				})
			}
		})
	}
}

func TestWorkloadConfigurationScanReportTimestampSchema(t *testing.T) {
	definitions := openapi.GetOpenAPIDefinitions(func(name string) spec.Ref {
		return spec.MustCreateRef("#/definitions/" + name)
	})

	scanSpec := definitions[v1beta1.WorkloadConfigurationScanSpec{}.OpenAPIModelName()].Schema
	metadata, ok := scanSpec.Properties["metadata"]
	require.True(t, ok, "scan spec must expose metadata")
	require.NotNil(t, metadata.Ref)
	require.NotContains(t, scanSpec.Required, "metadata", "scan metadata must remain optional")
	require.Equal(t, "#/definitions/"+v1beta1.WorkloadConfigurationScanMeta{}.OpenAPIModelName(), metadata.Ref.String())

	scanMeta := definitions[v1beta1.WorkloadConfigurationScanMeta{}.OpenAPIModelName()].Schema
	report, ok := scanMeta.Properties["report"]
	require.True(t, ok, "scan metadata must expose report")
	require.NotNil(t, report.Ref)
	require.Equal(t, "#/definitions/"+v1beta1.ReportMeta{}.OpenAPIModelName(), report.Ref.String())

	reportMeta := definitions[v1beta1.ReportMeta{}.OpenAPIModelName()].Schema
	createdAt, ok := reportMeta.Properties["createdAt"]
	require.True(t, ok, "report metadata must expose createdAt")
	require.Equal(t, []string{"string"}, createdAt.Type)
	require.Equal(t, "date-time", createdAt.Format)
	require.Contains(t, reportMeta.Required, "createdAt")
}
