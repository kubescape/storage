package dynamicpathdetector

// --- Identifier constants ---
// DynamicIdentifier matches exactly one path segment (like a single-segment wildcard).
// WildcardIdentifier matches zero or more path segments (like a glob **).
const (
	DynamicIdentifier  = "\u22ef" // U+22EF: ⋯
	WildcardIdentifier = "*"
)

// --- Collapse configuration ---

// CollapseConfig controls the threshold at which children of a trie node
// (under the given path Prefix) are collapsed into a dynamic or wildcard node.
type CollapseConfig struct {
	Prefix    string
	Threshold int
}

// DefaultCollapseConfigs defines per-prefix thresholds for path collapsing.
// Paths under these prefixes are collapsed when the number of unique children
// exceeds the threshold.
var DefaultCollapseConfigs = []CollapseConfig{
	{Prefix: "/etc", Threshold: 100},
	{Prefix: "/etc/apache2", Threshold: 5}, //this is mostly for our webapp standard test
	{Prefix: "/opt", Threshold: 5},
	{Prefix: "/var/run", Threshold: 3},
	{Prefix: "/app", Threshold: 1},
}

const OpenDynamicThreshold = 50
const EndpointDynamicThreshold = 100

var DefaultCollapseConfig = CollapseConfig{
	Prefix:    "/",
	Threshold: OpenDynamicThreshold,
}

// --- Types ---

type SegmentNode struct {
	SegmentName string
	Count       int
	Children    map[string]*SegmentNode
	Config      *CollapseConfig
}

type PathAnalyzer struct {
	root             *TrieNode
	identRoots       map[string]*TrieNode
	configs          []CollapseConfig
	defaultCfg       CollapseConfig
	collapseAdjacent bool
}

func NewTrieNode() *TrieNode {
	return &TrieNode{
		Children: make(map[string]*TrieNode),
	}
}

type TrieNode struct {
	Children map[string]*TrieNode
	Config   *CollapseConfig
	Count    int
}

func (sn *SegmentNode) IsNextDynamic() bool {
	_, exists := sn.Children[DynamicIdentifier]
	return exists
}
