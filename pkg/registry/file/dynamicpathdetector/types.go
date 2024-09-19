package dynamicpathdetector

const DynamicIdentifier string = "\u22ef"

type SegmentNode struct {
	SegmentName string
	Count       int
	Children    map[string]*SegmentNode
}

type PathAnalyzer struct {
	RootNodes map[string]*SegmentNode
	threshold int
}

func (sn *SegmentNode) IsNextDynamic() bool {
	_, exists := sn.Children[DynamicIdentifier]
	return exists
}
