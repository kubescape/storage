package dynamicpathdetector

import "strings"

// OpenProtection is the storage-side, generation-time mirror of a rule's
// profileDataRequired.opens matchers (node-agent typesv1.PatternObject kinds:
// exact / prefix / suffix / contains). It is the shared input to rule-aware
// collapse: each environment maps its own rule source — the rules CRD in
// cluster, MongoDB in the backend — into the union of these four matcher kinds,
// and ProtectedPrefixes turns them (with the raw opened paths) into the set of
// prefixes the analyzer must pin to literal so they survive generalisation.
//
// Reuse note: the canonical schema lives in node-agent
// (pkg/rulemanager/types/v1.PatternObject) and the projection/query side
// compiles it via objectcache.CompileSpec. node-agent imports storage (not the
// reverse), so this mirror is the lowest common home for the matcher used by
// BOTH the query side (was_path_opened) and this generation side; longer term
// the query side can delegate here to remove the duplication.
type OpenProtection struct {
	Exact    []string
	Prefix   []string
	Suffix   []string
	Contains []string
}

// Empty reports whether no matcher is declared, so callers can skip the work
// and preserve exact legacy collapse behaviour.
func (p OpenProtection) Empty() bool {
	return len(p.Exact)+len(p.Prefix)+len(p.Suffix)+len(p.Contains) == 0
}

// ProtectedPrefixes returns the prefixes to pin given the raw opened paths.
//
// Exact and Prefix carry a fixed leading directory, so they pin statically:
// their ancestor chain is kept literal even if the sensitive file was never
// opened during learning — which is what stops a busy sibling set from
// collapsing the parent (e.g. /etc → /etc/⋯) and spuriously covering a
// first-ever sensitive access.
//
// CRITICAL/KNOWN LIMITATION: Suffix and Contains have no fixed location, so they
// are resolved against the actually-opened paths. Any observed path that matches
// is pinned, keeping its ancestor chain literal.
// However, because this is learning-dependent, if NO path matching the matcher
// was opened during learning, the containing directory (if high-cardinality)
// will collapse (e.g. /etc/ssh → /etc/ssh/⋯). Consequently, a first-ever runtime
// access to a matching path (e.g. /etc/ssh/ssh_host_ed25519_key) will be covered
// by the wildcard and the rule will not fire. Suffix/Contains protection is
// therefore best-effort, and rule authors should steer toward Exact/Prefix
// matchers for paths where detection must be guaranteed.
func (p OpenProtection) ProtectedPrefixes(openPaths []string) []string {
	if p.Empty() {
		return nil
	}
	out := make([]string, 0, len(p.Exact)+len(p.Prefix))
	out = append(out, p.Exact...)
	out = append(out, p.Prefix...)
	if len(p.Suffix) > 0 || len(p.Contains) > 0 {
		for _, op := range openPaths {
			if p.matchesUnanchored(op) {
				out = append(out, op) // pin this open's ancestor chain
			}
		}
	}
	return out
}

// matchesUnanchored reports whether path matches a Suffix or Contains matcher —
// the location-independent kinds that must be resolved against observed paths.
func (p OpenProtection) matchesUnanchored(path string) bool {
	for _, s := range p.Suffix {
		if s != "" && strings.HasSuffix(path, s) {
			return true
		}
	}
	for _, c := range p.Contains {
		if c != "" && strings.Contains(path, c) {
			return true
		}
	}
	return false
}
