package dynamicpathdetector

const dynamicIdentifier string = "<dynamic>"

const threshold = 100

type SegmentNode struct {
	SegmentName string
	Count       int
	Children    map[string]*SegmentNode
}

type PathAnalyzer struct {
	rootNodes map[string]*SegmentNode
}

func (sn *SegmentNode) IsNextDynamic() bool {
	_, exists := sn.Children[dynamicIdentifier]
	return exists
}
