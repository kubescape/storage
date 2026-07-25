package dynamicpathdetector

// --- Identifier constants ---
// DynamicIdentifier matches exactly one path segment (single-segment wildcard),
// and in exec args matches exactly one whole argument.
// WildcardIdentifier matches zero-or-more path segments (glob-style **). It is a
// PATH/OPENS wildcard only — in exec args a "*" is a plain literal character (a
// process is frequently invoked with a literal "*", e.g. an unexpanded glob).
// ExecArgsWildcard is the exec-args zero-or-more wildcard: a standalone argv
// token that absorbs zero or more whole arguments. It is a dedicated sentinel
// (doubled U+22EF) precisely so it cannot collide with any real argv token —
// the same collision-avoidance rationale behind DynamicIdentifier. Exec args
// therefore need no escaping: every other byte, including "*", is literal.
const (
	DynamicIdentifier  string = "⋯"  // U+22EF: ⋯ (one segment / one arg)
	WildcardIdentifier string = "*"  // zero-or-more path segments (opens only)
	ExecArgsWildcard   string = "⋯⋯" // zero-or-more whole exec args
)

// --- Default collapse thresholds ---
// OpenDynamicThreshold is the fallback threshold used by AnalyzeOpens when
// no more-specific CollapseConfig matches the walked path prefix.
// EndpointDynamicThreshold is the counterpart for AnalyzeEndpoints.
// NetworkIPGroupThreshold is the count threshold above which a group of
// NetworkNeighbor entries differing only by IP gets CIDR-collapsed.
// NetworkCIDRFloorBits is the minimum CIDR prefix length (maximum breadth)
// a single aggregated block may have.
// NetworkMaxCIDRSplitBits caps, PER prefix, how far a single cover block broader
// than the floor is split into floor-width children: up to 2^NetworkMaxCIDRSplitBits
// blocks (4096 here). A prefix whose split would exceed that is kept as-is rather
// than exploding the entry list. This bounds ONE block's fan-out (not the sum
// across a group — the whole neighborhood is separately capped by
// MaxNetworkNeighborhoodSize); it only bites when a held pass-through block is
// much broader than a tightened floor (e.g. a /16 under a /28 floor -> 4096
// children; a /16 under a /24 floor is only 256). Not currently exposed as a
// CollapseConfiguration field.
const (
	OpenDynamicThreshold     = 50
	EndpointDynamicThreshold = 100
	NetworkIPGroupThreshold  = 50
	NetworkCIDRFloorBits     = 24
	NetworkMaxCIDRSplitBits  = 12
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
}

func (sn *SegmentNode) IsNextDynamic() bool {
	_, exists := sn.Children[DynamicIdentifier]
	return exists
}
