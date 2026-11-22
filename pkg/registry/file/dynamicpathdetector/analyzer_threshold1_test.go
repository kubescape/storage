package dynamicpathdetector

import (
	"testing"

	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

// A global threshold of 1 collapses everything under the root. The result is
// still anchored: "/*", not the bare catch-all "*".
func TestAnalyzeOpen_GlobalThreshold1KeepsRootAnchor(t *testing.T) {
	got, err := AnalyzeOpen("/etc/passwd", NewPathAnalyzer(1))
	if err != nil {
		t.Fatal(err)
	}
	if want := "/" + WildcardIdentifier; got != want {
		t.Errorf("AnalyzeOpen(/etc/passwd) with threshold 1 = %q, want %q", got, want)
	}
}

// endpointDynamicThreshold: 1 is accepted by CollapseConfiguration validation.
// The collapsed endpoint has to stay parseable, or the next save drops it.
func TestAnalyzeEndpoints_GlobalThreshold1SurvivesReanalysis(t *testing.T) {
	settings := CollapseSettingsFromCRD(&types.CollapseConfiguration{
		Spec: types.CollapseConfigurationSpec{EndpointDynamicThreshold: 1},
	})
	newAnalyzer := func() *PathAnalyzer {
		return NewPathAnalyzerWithConfigs(settings.EndpointDynamicThreshold, nil)
	}
	want := ":443/" + WildcardIdentifier

	endpoints := []types.HTTPEndpoint{{Endpoint: ":443/api/users", Methods: []string{"GET"}}}
	first := AnalyzeEndpoints(&endpoints, newAnalyzer())
	if len(first) != 1 || first[0].Endpoint != want {
		t.Fatalf("first pass = %v, want one endpoint %q", first, want)
	}

	second := AnalyzeEndpoints(&first, newAnalyzer())
	if len(second) != 1 || second[0].Endpoint != want {
		t.Errorf("second pass = %v, want one endpoint %q", second, want)
	}
}
