package dynamicpathdetector

import (
	pathUtils "path"
	"strings"
)

func NewPathAnalyzer() *PathAnalyzer {
	return &PathAnalyzer{
		RootNodes: make(map[string]*SegmentNode),
		threshold: 100,
	}
}
func (ua *PathAnalyzer) AnalyzePath(path, identifier string) (string, error) {
	path = pathUtils.Clean(path)
	node, exists := ua.RootNodes[identifier]
	if !exists {
		node = &SegmentNode{
			SegmentName: identifier,
			Count:       0,
			Children:    make(map[string]*SegmentNode),
		}
		ua.RootNodes[identifier] = node
	}

	segments := strings.Split(strings.Trim(path, "/"), "/")

	return ua.processSegments(node, segments), nil
}

func (ua *PathAnalyzer) processSegments(node *SegmentNode, segments []string) string {
	resultPath := []string{}
	currentNode := node
	for _, segment := range segments {
		currentNode = ua.processSegment(currentNode, segment)
		ua.updateNodeStats(currentNode)
		resultPath = append(resultPath, currentNode.SegmentName)
	}
	return "/" + strings.Join(resultPath, "/")

}

func (ua *PathAnalyzer) processSegment(node *SegmentNode, segment string) *SegmentNode {

	switch {
	case segment == dynamicIdentifier:
		return ua.handleDynamicSegment(node)
	case KeyInMap(node.Children, segment) || node.IsNextDynamic():
		child, exists := node.Children[segment]
		return ua.handleExistingSegment(node, child, exists)
	default:
		return ua.handleNewSegment(node, segment)

	}
}

func (ua *PathAnalyzer) handleExistingSegment(node *SegmentNode, child *SegmentNode, exists bool) *SegmentNode {
	if exists {
		return child
	} else {
		return node.Children[dynamicIdentifier]
	}
}

func (ua *PathAnalyzer) handleNewSegment(node *SegmentNode, segment string) *SegmentNode {
	node.Count++
	newNode := &SegmentNode{
		SegmentName: segment,
		Count:       0,
		Children:    make(map[string]*SegmentNode),
	}
	node.Children[segment] = newNode
	return newNode
}

func (ua *PathAnalyzer) handleDynamicSegment(node *SegmentNode) *SegmentNode {
	if dynamicChild, exists := node.Children[dynamicIdentifier]; exists {
		return dynamicChild
	} else {
		return ua.createDynamicNode(node)
	}
}

func (ua *PathAnalyzer) createDynamicNode(node *SegmentNode) *SegmentNode {
	dynamicNode := &SegmentNode{
		SegmentName: dynamicIdentifier,
		Count:       0,
		Children:    make(map[string]*SegmentNode),
	}

	// Copy all existing children to the new dynamic node
	for _, child := range node.Children {
		shallowChildrenCopy(child, dynamicNode)
	}

	// Replace all children with the new dynamic node
	node.Children = map[string]*SegmentNode{
		dynamicIdentifier: dynamicNode,
	}

	return dynamicNode
}

func (ua *PathAnalyzer) updateNodeStats(node *SegmentNode) {
	if node.Count > ua.threshold && !node.IsNextDynamic() {

		dynamicChild := &SegmentNode{
			SegmentName: dynamicIdentifier,
			Count:       0,
			Children:    make(map[string]*SegmentNode),
		}

		// Copy all descendants
		for _, child := range node.Children {
			shallowChildrenCopy(child, dynamicChild)
		}

		node.Children = map[string]*SegmentNode{
			dynamicIdentifier: dynamicChild,
		}
	}
}

func shallowChildrenCopy(src, dst *SegmentNode) {
	for segmentName := range src.Children {
		if !KeyInMap(dst.Children, segmentName) {
			dst.Children[segmentName] = src.Children[segmentName]
		} else {
			dst.Children[segmentName].Count += src.Children[segmentName].Count
			shallowChildrenCopy(src.Children[segmentName], dst.Children[segmentName])
		}
	}
}

func KeyInMap[T any](TestMap map[string]T, key string) bool {
	_, ok := TestMap[key]
	return ok
}
