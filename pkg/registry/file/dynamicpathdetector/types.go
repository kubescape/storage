package dynamicpathdetector

import "math"

// --- Identifier constants ---
// DynamicIdentifier matches exactly one path segment (single-segment wildcard).
// WildcardIdentifier matches zero-or-more path segments (glob-style **).
const (
	DynamicIdentifier  string = "⋯" // U+22EF: ⋯
	WildcardIdentifier string = "*"
)

// NeverCollapseThreshold is an effective threshold so high a node's children
// can never exceed it, i.e. the node is pinned to literal entries. Used for
// rule-protected prefixes (see PathAnalyzer.protected): keeping a sensitive
// prefix and its ancestors literal is what lets anomaly rules such as R0010
// ("unexpected /etc/shadow access") distinguish a never-seen path from a
// generalised wildcard like /etc/⋯ or /⋯/⋯ that would otherwise cover it.
const NeverCollapseThreshold = math.MaxInt

// PinnedSubtreeBudget is the maximum number of literal children a protected/pinned
// node is allowed to accumulate before we fall back to collapsing it. This prevents
// size blowup and "TooLarge" status (which clears the entire profile spec downstream)
// when a protected directory (like /etc) receives a very high number of unique opens.
const PinnedSubtreeBudget = 500

// --- Default collapse thresholds ---
// OpenDynamicThreshold is the fallback threshold used by AnalyzeOpens when
// no more-specific CollapseConfig matches the walked path prefix.
// EndpointDynamicThreshold is the counterpart for AnalyzeEndpoints.
const (
	OpenDynamicThreshold     = 50
	EndpointDynamicThreshold = 100
)

// --- Collapse configuration ---
// CollapseConfig controls the threshold at which children of a trie node
// (under the given path Prefix) are collapsed into a dynamic node (⋯).
// Longest-prefix wins at analysis time.
type CollapseConfig struct {
	Prefix    string
	Threshold int
}

// defaultCollapseConfigs carries the per-prefix thresholds we've found
// useful in practice. These are the defaults wired into AnalyzeOpens; a
// caller can pass a different slice via NewPathAnalyzerWithConfigs if
// they want to tune for their workload. Unexported so callers cannot
// mutate the package-level slice — access via DefaultCollapseConfigs().
var defaultCollapseConfigs = []CollapseConfig{
	{Prefix: "/etc", Threshold: 100},
	{Prefix: "/etc/apache2", Threshold: 50}, // tuned for the webapp standard test
	{Prefix: "/opt", Threshold: 50},
	{Prefix: "/var/run", Threshold: 50},
	{Prefix: "/app", Threshold: 50}, // any variation under /app collapses at 50 unique children
}

// DefaultCollapseConfigs returns a defensive copy of the package-level
// default per-prefix collapse thresholds. Callers that mutate the result
// will not affect the package state or other callers.
func DefaultCollapseConfigs() []CollapseConfig {
	return append([]CollapseConfig(nil), defaultCollapseConfigs...)
}

// defaultCollapseConfig is the package-private global fallback used when
// no CollapseConfig prefix matches the walked path. Exposed via the
// DefaultCollapseConfig() accessor so callers can't mutate the package
// state and silently corrupt every analyzer constructed afterward.
// CodeRabbit upstream PR #323 finding #3.
var defaultCollapseConfig = CollapseConfig{
	Prefix:    "/",
	Threshold: OpenDynamicThreshold,
}

// DefaultCollapseConfig returns a value copy of the package-private
// fallback. Mutating the returned struct does not affect package state.
// The accessor pattern matches DefaultCollapseConfigs() — both protect
// the threshold-tuning surface from accidental cross-test or
// cross-caller corruption.
func DefaultCollapseConfig() CollapseConfig {
	return defaultCollapseConfig
}

// --- Trie types ---

type SegmentNode struct {
	SegmentName string
	Count       int
	Children    map[string]*SegmentNode
}

type PathAnalyzer struct {
	RootNodes  map[string]*SegmentNode
	threshold  int              // fallback threshold when no config matches
	configs    []CollapseConfig // per-prefix overrides; longest prefix wins
	defaultCfg CollapseConfig   // explicit fallback; equivalent to {Prefix:"/", Threshold: threshold}

	// Rule-protected prefixes, precomputed into two sets so protectedNode is
	// O(1) for the common (non-protected) node and never scales with the number
	// of protected prefixes. Both nil when no protection is configured.
	//   pinAncestors: every dir-prefix (incl. self) of every protected prefix —
	//     e.g. "/etc/shadow" contributes {"/", "/etc", "/etc/shadow"}. A node
	//     whose path is in here is an ancestor-or-self of a protected prefix and
	//     must stay literal so no wildcard forms at/above the sensitive level.
	//   protectedRoots: the protected prefixes themselves, used to detect nodes
	//     *inside* a protected subtree (which must also stay literal).
	pinAncestors   map[string]struct{}
	protectedRoots map[string]struct{}
}

func (sn *SegmentNode) IsNextDynamic() bool {
	_, exists := sn.Children[DynamicIdentifier]
	return exists
}
