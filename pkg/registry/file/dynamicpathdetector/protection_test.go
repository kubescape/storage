package dynamicpathdetector

import (
	"fmt"
	"testing"
)

// genWith builds the trie from paths then re-analyzes each to collect the
// final generalized pattern set (mirrors AnalyzeOpens' two-pass shape).
func genWith(a *PathAnalyzer, paths []string) map[string]struct{} {
	for _, p := range paths {
		_, _ = AnalyzeOpen(p, a)
	}
	out := map[string]struct{}{}
	for _, p := range paths {
		r, _ := AnalyzeOpen(p, a)
		out[r] = struct{}{}
	}
	return out
}

func coveredBy(pats map[string]struct{}, target string) string {
	for p := range pats {
		if CompareDynamic(p, target) {
			return p
		}
	}
	return ""
}

// TestProtectedPrefixKeepsSensitiveDetectable is the regression for the R0010
// false-negative: with /etc far over the collapse threshold, an unprotected
// profile folds /etc into /etc/⋯ (or the root into /⋯/⋯), which spuriously
// covers a never-seen /etc/shadow.evil and makes was_path_opened return true.
// Protecting the rule prefix /etc/shadow must keep that path space literal
// while still collapsing unrelated high-cardinality trees like /proc.
func TestProtectedPrefixKeepsSensitiveDetectable(t *testing.T) {
	paths := []string{"/etc/shadow"}
	for i := 0; i < 80; i++ { // /etc way over OpenDynamicThreshold
		paths = append(paths, fmt.Sprintf("/etc/file%d", i))
	}
	for d := 0; d < 80; d++ { // /proc huge: must still collapse
		for f := 0; f < 80; f++ {
			paths = append(paths, fmt.Sprintf("/proc/%d/task%d", d, f))
		}
	}

	// Precondition: without protection the novel sensitive path IS covered.
	plain := genWith(NewPathAnalyzer(OpenDynamicThreshold), paths)
	if by := coveredBy(plain, "/etc/shadow.evil"); by == "" {
		t.Fatalf("precondition failed: unprotected profile should cover /etc/shadow.evil (got patterns %v)", keysOf(plain))
	}

	// With protection of /etc/shadow.
	prot := genWith(
		NewPathAnalyzerWithConfigsAndProtection(OpenDynamicThreshold, nil, []string{"/etc/shadow"}),
		paths,
	)
	if by := coveredBy(prot, "/etc/shadow.evil"); by != "" {
		t.Errorf("novel /etc/shadow.evil still covered by %q — protection failed", by)
	}
	if _, ok := prot["/etc/shadow"]; !ok {
		t.Errorf("expected /etc/shadow retained as a literal; got %v", keysOf(prot))
	}
	// Exact learned /etc/shadow is still recognised (no false positive on baseline).
	if coveredBy(prot, "/etc/shadow") == "" {
		t.Errorf("expected learned /etc/shadow to be matched")
	}
	// /proc must still collapse — protection of /etc must not disable bloat
	// control elsewhere. 6400 proc paths must not survive as literals.
	if len(prot) > 200 {
		t.Errorf("expected /proc subtree to collapse (bounded pattern set); got %d patterns", len(prot))
	}
}

func keysOf(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestOpenProtectionExactSuffixContains exercises all four matcher kinds from a
// realistic R0010-style profileDataRequired against a noisy, high-cardinality
// open set, asserting that never-seen sensitive paths stay detectable while
// unrelated trees (/proc) still collapse.
func TestOpenProtectionExactSuffixContains(t *testing.T) {
	prot := OpenProtection{
		Exact:    []string{"/etc/sudoers"},
		Prefix:   []string{"/etc/shadow", "/etc/sudoers.d/"},
		Suffix:   []string{"_key"},      // ssh host keys
		Contains: []string{"/.ssh/"},    // per-user ssh material, location unknown
	}

	var opens []string
	opens = append(opens,
		"/etc/sudoers",
		"/etc/sudoers.d/90-cloud-init",
		"/etc/ssh/ssh_host_rsa_key",      // suffix _key
		"/home/alice/.ssh/id_rsa",        // contains /.ssh/
	)
	for i := 0; i < 90; i++ { // /etc way over threshold
		opens = append(opens, fmt.Sprintf("/etc/file%d", i))
	}
	for u := 0; u < 60; u++ { // many home users (would collapse /home)
		opens = append(opens, fmt.Sprintf("/home/user%d/.bashrc", u))
	}
	for d := 0; d < 80; d++ { // /proc must still collapse
		for f := 0; f < 80; f++ {
			opens = append(opens, fmt.Sprintf("/proc/%d/task%d", d, f))
		}
	}

	prefixes := prot.ProtectedPrefixes(opens)
	got := genWith(NewPathAnalyzerWithConfigsAndProtection(OpenDynamicThreshold, nil, prefixes), opens)

	// Never-seen sensitive paths must NOT be covered by any wildcard.
	novel := map[string]string{
		"/etc/shadow.evil":                 "prefix /etc/shadow",
		"/etc/sudoers.bak":                 "exact /etc/sudoers (pins /etc)",
		"/etc/sudoers.d/99-evil":           "prefix /etc/sudoers.d/",
		"/etc/ssh/ssh_host_ed25519_key":    "suffix _key (pins /etc/ssh)",
		"/home/alice/.ssh/evil_authorized": "contains /.ssh/ (pins alice's .ssh)",
	}
	for p, why := range novel {
		if by := coveredBy(got, p); by != "" {
			t.Errorf("novel %q covered by %q — protection failed (%s)", p, by, why)
		}
	}

	// Bloat control preserved: /proc (6400 paths) must still collapse.
	if len(got) > 400 {
		t.Errorf("expected /proc to collapse (bounded pattern set); got %d patterns", len(got))
	}

	// Sanity: with no protection the same set DOES cover a novel sensitive path.
	plain := genWith(NewPathAnalyzer(OpenDynamicThreshold), opens)
	if coveredBy(plain, "/etc/shadow.evil") == "" {
		t.Fatalf("precondition: unprotected run should cover /etc/shadow.evil")
	}
}
