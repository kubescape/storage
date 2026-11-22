package v1alpha1

import (
	"os"
	"reflect"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestNodeStatusHTTPBlockRoundTripAndDeepCopy(t *testing.T) {
	data, err := os.ReadFile(exampleDir + "sandboxpolicynodestatus.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var report SandboxPolicyNodeStatus
	if err = yaml.UnmarshalStrict(data, &report); err != nil {
		t.Fatal(err)
	}
	want := HTTPNodeStatus{BoundPods: 3, PreAttachBypass: 1, NeedRestart: []string{"agents/agent-7c9d-x2x"}, Gate: HTTPGateRedirecting}
	if report.Status == nil || report.Status.HTTP == nil || !reflect.DeepEqual(*report.Status.HTTP, want) {
		t.Fatalf("http block = %+v", report.Status.HTTP)
	}
	c := report.DeepCopy()
	c.Status.HTTP.NeedRestart[0] = "changed"
	c.Status.HTTP.Gate = HTTPGateFailingOpen
	if !reflect.DeepEqual(*report.Status.HTTP, want) {
		t.Fatal("deep copy shares the http block")
	}
	for gate, value := range map[HTTPGate]string{HTTPGateRedirecting: "redirecting", HTTPGateFailingOpen: "failing_open", HTTPGateFailingClosed: "failing_closed", HTTPGateNotBound: "not_bound"} {
		if string(gate) != value {
			t.Fatalf("gate constant %q, want %q", gate, value)
		}
	}
	out, err := yaml.Marshal(HTTPNodeStatus{Gate: HTTPGateNotBound})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != "boundPods: 0\ngate: not_bound\nnodeControllerDegraded: false\npreAttachBypass: 0\n" {
		t.Fatalf("zero values must be written, got:\n%s", got)
	}
}
