package dynamicpathdetector

import "testing"

// TestAnalyzePath_EmptyDoesNotMintDot pins the "." mint: path.Clean("") is
// ".", so an empty open path in a chunk became a literal "." entry in the
// consolidated profile (observed live: node-exporter's failed openat("")).
func TestAnalyzePath_EmptyDoesNotMintDot(t *testing.T) {
	ua := NewPathAnalyzer(100)
	got, err := ua.AnalyzePath("", "opens")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("AnalyzePath(\"\") = %q, want empty", got)
	}
}
