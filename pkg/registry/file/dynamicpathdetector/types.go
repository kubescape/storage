package dynamicpathdetector

const DynamicIdentifier string = "\u22ef"

type CollapseConfig struct {
	Prefix    string
	Threshold int
}

type SegmentNode struct {
	SegmentName string
	Count       int
	Children    map[string]*SegmentNode
	Config      *CollapseConfig // Configuration that applies from this node downwards
}

type PathAnalyzer struct {
	root       *TrieNode
	identRoots map[string]*TrieNode
	configs    []CollapseConfig
	defaultCfg CollapseConfig
}

func NewTrieNode() *TrieNode {
	return &TrieNode{
		Children: make(map[string]*TrieNode),
	}
}

type TrieNode struct {
	Children map[string]*TrieNode
	Config   *CollapseConfig // Configuration that applies from this node downwards
	Count    int             // Number of paths passing through this node
}

func (sn *SegmentNode) IsNextDynamic() bool {
	_, exists := sn.Children[DynamicIdentifier]
	return exists
}
