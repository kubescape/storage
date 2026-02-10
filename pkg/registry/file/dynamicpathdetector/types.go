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
	RootNodes             map[string]*SegmentNode
	threshold             int // Default threshold
	DefaultCollapseConfig *CollapseConfig
	configRoot            *SegmentNode // Trie for storing CollapseConfigs
}

func (sn *SegmentNode) IsNextDynamic() bool {
	_, exists := sn.Children[DynamicIdentifier]
	return exists
}
