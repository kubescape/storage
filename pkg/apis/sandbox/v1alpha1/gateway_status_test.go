package v1alpha1

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

func TestSandboxGatewayNodeStatusScheme(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"SandboxGatewayNodeStatus", "SandboxGatewayNodeStatusList"} {
		if !scheme.Recognizes(SchemeGroupVersion.WithKind(kind)) {
			t.Fatalf("%s is not registered", kind)
		}
	}
}

func TestSandboxGatewayNodeStatusExampleRoundTripAndDeepCopy(t *testing.T) {
	data, err := os.ReadFile(exampleDir + "sandboxgatewaynodestatus.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var report SandboxGatewayNodeStatus
	if err = yaml.UnmarshalStrict(data, &report); err != nil {
		t.Fatal(err)
	}
	s := report.Status
	if s == nil || !s.Envoy.Up || s.Envoy.Version != "1.40.2" || !s.Processor.Up || s.Processor.Evaluator != GatewayEvaluatorAvailable ||
		s.Hosts != (GatewayHostCounts{Exact: 12, Learned: 3, Cap: 256}) || !reflect.DeepEqual(s.RejectedPatterns, []string{"*.amazonaws.com"}) ||
		s.DecryptValidUntil == nil || s.ObservedAt.IsZero() {
		t.Fatalf("status = %+v", s)
	}
	var authored, after map[string]any
	if err = yaml.Unmarshal(data, &authored); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	// False and zero are facts about the node, so they are written, not omitted.
	if !reflect.DeepEqual(authored["status"], after["status"]) {
		t.Fatalf("status changed in a round trip:\nwant %v\ngot  %v", authored["status"], after["status"])
	}
	if _, ok := after["spec"]; ok {
		t.Fatal("the kind has no spec")
	}
	c := report.DeepCopy()
	c.Status.RejectedPatterns[0] = "changed"
	*c.Status.DecryptValidUntil = c.Status.ObservedAt
	if s.RejectedPatterns[0] != "*.amazonaws.com" || s.DecryptValidUntil.Equal(&s.ObservedAt) {
		t.Fatal("deep copy shares memory with the original")
	}
	if GatewayEvaluatorUnavailable != "unavailable" {
		t.Fatalf("evaluator constant %q", GatewayEvaluatorUnavailable)
	}
}
