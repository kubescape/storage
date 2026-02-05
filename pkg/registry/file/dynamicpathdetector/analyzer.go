package dynamicpathdetector

import (
	"path"
	"strings"
)

func NewPathAnalyzer(threshold int) *PathAnalyzer {
	return &PathAnalyzer{
		RootNodes: make(map[string]*SegmentNode),
		threshold: threshold,
	}
}

func (ua *PathAnalyzer) AnalyzePath(p, identifier string) (string, error) {
	p = path.Clean(p)
	node, exists := ua.RootNodes[identifier]
	if !exists {
		node = &SegmentNode{
			SegmentName: identifier,
			Count:       0,
			Children:    make(map[string]*SegmentNode),
		}
		ua.RootNodes[identifier] = node
	}
	processedPath := ua.processSegments(node, p)
	return CollapseAdjacentDynamicIdentifiers(processedPath), nil
}

func (ua *PathAnalyzer) processSegments(node *SegmentNode, p string) string {
	var result strings.Builder
	currentNode := node
	i := 0
	for {
		start := i
		for i < len(p) && p[i] != '/' {
			i++
		}
		segment := p[start:i]
		currentNode = ua.processSegment(currentNode, segment)
		ua.updateNodeStats(currentNode)
		result.WriteString(currentNode.SegmentName)
		i++
		if len(p) < i {
			break
		}
		result.WriteByte('/')
	}
	return result.String()
}

func (ua *PathAnalyzer) processSegment(node *SegmentNode, segment string) *SegmentNode {
	if segment == DynamicIdentifier {
		return ua.handleDynamicSegment(node)
	} else if node.IsNextDynamic() {
		if len(node.Children) > 1 {
			temp := node.Children[DynamicIdentifier]
			node.Children = map[string]*SegmentNode{}
			node.Children[DynamicIdentifier] = temp
		}
		return node.Children[DynamicIdentifier]
	} else if child, exists := node.Children[segment]; exists {
		return child
	} else {
		return ua.handleNewSegment(node, segment)
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
	if dynamicChild, exists := node.Children[DynamicIdentifier]; exists {
		return dynamicChild
	} else {
		return ua.createDynamicNode(node)
	}
}

func (ua *PathAnalyzer) createDynamicNode(node *SegmentNode) *SegmentNode {
	dynamicNode := &SegmentNode{
		SegmentName: DynamicIdentifier,
		Count:       0,
		Children:    make(map[string]*SegmentNode),
	}

	// Copy all existing children to the new dynamic node
	for _, child := range node.Children {
		shallowChildrenCopy(child, dynamicNode)
	}

	// Replace all children with the new dynamic node
	node.Children = map[string]*SegmentNode{
		DynamicIdentifier: dynamicNode,
	}

	return dynamicNode
}

func (ua *PathAnalyzer) updateNodeStats(node *SegmentNode) {
	if node.Count > ua.threshold && !node.IsNextDynamic() {
		dynamicChild := &SegmentNode{
			SegmentName: DynamicIdentifier,
			Count:       0,
			Children:    make(map[string]*SegmentNode),
		}

		// Copy all descendants
		for _, child := range node.Children {
			shallowChildrenCopy(child, dynamicChild)
		}

		node.Children = map[string]*SegmentNode{
			DynamicIdentifier: dynamicChild,
		}
	}
}

func shallowChildrenCopy(src, dst *SegmentNode) {
	for segmentName := range src.Children {
		if _, ok := dst.Children[segmentName]; !ok {
			dst.Children[segmentName] = src.Children[segmentName]
		} else {
			dst.Children[segmentName].Count += src.Children[segmentName].Count
			shallowChildrenCopy(src.Children[segmentName], dst.Children[segmentName])
		}
	}
}

func CollapseAdjacentDynamicIdentifiers(p string) string {
	segments := strings.Split(p, "/")
	var result []string
	inDynamicSequence := false

	for i := 0; i < len(segments); i++ {
		isDynamic := segments[i] == DynamicIdentifier

		if isDynamic && !inDynamicSequence {
			// Check if this starts a sequence of at least two dynamic identifiers
			isSequence := false
			for j := i + 1; j < len(segments); j++ {
				if segments[j] == DynamicIdentifier {
					isSequence = true
					break
				}
			}

			if isSequence {
				inDynamicSequence = true
				result = append(result, "*")
			} else {
				result = append(result, segments[i])
			}
		} else if isDynamic && inDynamicSequence {
			// Continue sequence, do nothing as '*' is already added
			continue
		} else {
			inDynamicSequence = false
			result = append(result, segments[i])
		}
	}
	return strings.Join(result, "/")
}

func CompareDynamic(dynamicPath, regularPath string) bool {
	dynamicSegments := strings.Split(dynamicPath, "/")
	regularSegments := strings.Split(regularPath, "/")

	return compareSegments(dynamicSegments, regularSegments)
}

func compareSegments(dynamic, regular []string) bool {
	if len(dynamic) == 0 {
		return len(regular) == 0
	}

	if dynamic[0] == "*" {
		if len(dynamic) == 1 {
			return true
		}
		nextDynamic := dynamic[1]
		for i := range regular {

			match := nextDynamic == DynamicIdentifier || (i < len(regular) && regular[i] == nextDynamic)

			if match && compareSegments(dynamic[1:], regular[i:]) {
				return true
			}
		}
		return false
	}

	if len(regular) == 0 {
		return false
	}

	if dynamic[0] == DynamicIdentifier || dynamic[0] == regular[0] {
		return compareSegments(dynamic[1:], regular[1:])
	}

	return false
}
