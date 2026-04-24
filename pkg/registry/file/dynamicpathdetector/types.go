package dynamicpathdetector

// --- Identifier constants ---
// DynamicIdentifier matches exactly one path segment (single-segment wildcard).
// WildcardIdentifier matches zero-or-more path segments (glob-style **).
const (
	DynamicIdentifier  string = "⋯" // U+22EF: ⋯
	WildcardIdentifier string = "*"
)

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

// DefaultCollapseConfigs carries the per-prefix thresholds we've found
// useful in practice. These are the defaults wired into AnalyzeOpens; a
// caller can pass a different slice via NewPathAnalyzerWithConfigs if
// they want to tune for their workload.
var DefaultCollapseConfigs = []CollapseConfig{
	{Prefix: "/etc", Threshold: 100},
	{Prefix: "/etc/apache2", Threshold: 50}, // tuned for the webapp standard test
	{Prefix: "/opt", Threshold: 50},
	{Prefix: "/var/run", Threshold: 50},
	{Prefix: "/app", Threshold: 50}, // any variation under /app collapses immediately
}

// DefaultCollapseConfig is the global fallback used when no CollapseConfig
// prefix matches the walked path.
var DefaultCollapseConfig = CollapseConfig{
	Prefix:    "/",
	Threshold: OpenDynamicThreshold,
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
