package dynamicpathdetector

const dynamicIdentifier string = "<dynamic>"

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
	_, exists := sn.Children[dynamicIdentifier]
	return exists
}
