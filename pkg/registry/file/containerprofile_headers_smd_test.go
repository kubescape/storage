package file

import (
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/generated/openapi"
	"github.com/stretchr/testify/require"
	"k8s.io/kube-openapi/pkg/schemaconv"
	"k8s.io/kube-openapi/pkg/validation/spec"
	smdschema "sigs.k8s.io/structured-merge-diff/v6/schema"
	"sigs.k8s.io/structured-merge-diff/v6/typed"
)

// TestHTTPEndpointHeaders_SMDConversion pins the fix for the Test_09
// (FalsePositiveTest) managed-fields failure.
//
// HTTPEndpoint.Headers is a json.RawMessage that node-agent fills with a JSON
// object (e.g. {"Content-Type":["application/json"],"Host":["prometheus:9090"]}),
// but the generated OpenAPI published it as {type: string, format: byte}. The
// apiserver builds its server-side-apply TypeConverter from this OpenAPI
// (GenericAPIServer.getOpenAPIModels -> managedfields.NewTypeConverter ->
// schemaconv.ToSchemaFromOpenAPI), so converting a ContainerProfile whose
// endpoints carry headers failed with
//
//	.spec.endpoints[endpoint="..."].headers: expected string, got <object>
//
// That managed-fields conversion error kept the profile from completing, so
// node-agent kept alerting on the non-enforcing profile (16 false positives).
//
// Headers is now schema-typed as a preserve-unknown-fields object, so the exact
// conversion the apiserver performs accepts the object. This test drives the
// real generated OpenAPI through the same schemaconv path and fails on the
// pre-fix string/byte schema.
func TestHTTPEndpointHeaders_SMDConversion(t *testing.T) {
	defs := openapi.GetOpenAPIDefinitions(func(string) spec.Ref { return spec.Ref{} })
	epDef, ok := defs[v1beta1.HTTPEndpoint{}.OpenAPIModelName()]
	require.True(t, ok, "HTTPEndpoint must be present in the generated OpenAPI definitions")

	// The published schema for headers must be an arbitrary object, not a
	// scalar string: json.RawMessage carries a JSON object, and smd cannot
	// represent an object against a scalar-string type.
	headers := epDef.Schema.Properties["headers"]
	require.Equal(t, []string{"object"}, []string(headers.Type), "headers must be an object, not string/byte")
	require.Equal(t, true, headers.Extensions["x-kubernetes-preserve-unknown-fields"],
		"headers must preserve unknown fields so smd can represent the raw JSON")

	// HTTPEndpoint has no cross-type $refs (all properties are inline scalars
	// plus the headers object), so a single-entry model map is a faithful,
	// self-contained input for the same schemaconv the apiserver runs.
	epSchema := epDef.Schema
	models := map[string]*spec.Schema{"HTTPEndpoint": &epSchema}
	typeSchema, err := schemaconv.ToSchemaFromOpenAPI(models, false)
	require.NoError(t, err)

	parser := typed.Parser{Schema: smdschema.Schema{Types: typeSchema.Types}}
	endpoint := map[string]interface{}{
		"endpoint": ":9090/-/healthy",
		"internal": false,
		"headers": map[string]interface{}{
			"Content-Type": []interface{}{"application/json"},
			"Host":         []interface{}{"prometheus:9090"},
		},
	}

	_, err = parser.Type("HTTPEndpoint").FromUnstructured(endpoint)
	require.NoError(t, err,
		`an endpoint whose headers is a JSON object must convert to smd-typed; pre-fix this failed with "expected string"`)
}
