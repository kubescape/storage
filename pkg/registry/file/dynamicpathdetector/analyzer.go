package dynamicpathdetector

import (
	"path"
	"strings"
)

// This function builds a tree of nodes

var CollapseConfigs = []CollapseConfig{
	{
		Prefix:    "/etc",
		Threshold: 50,
	},
	{
		Prefix:    "/opt",
		Threshold: 5,
	},
	{
		Prefix:    "/var/run", // here we have the special case that we treat two segments of the path as one
		Threshold: 3,
	},
	{
		Prefix:    "/app",
		Threshold: 1, // Now 1 has the special treatment that it IMMEDIATELY collapses everything into /$prefix/* , meaning it just matches everything
	},
}

func NewPathAnalyzer(threshold int) *PathAnalyzer {
	defaultConfig := &CollapseConfig{
		Prefix:    "/",
		Threshold: threshold,
	}
	analyzer := &PathAnalyzer{
		RootNodes:             make(map[string]*SegmentNode),
		threshold:             threshold,
		DefaultCollapseConfig: defaultConfig,
		configRoot: &SegmentNode{
			SegmentName: "/",
			Children:    make(map[string]*SegmentNode),
			Config:      defaultConfig,
		},
	}
	for i := range CollapseConfigs {
		analyzer.addConfig(&CollapseConfigs[i])
	}
	return analyzer
}

func (ua *PathAnalyzer) addConfig(config *CollapseConfig) {
	node := ua.configRoot
	segments := strings.Split(strings.Trim(config.Prefix, "/"), "/")
	if segments[0] == "" { // Handle root prefix "/"
		return
	}
	for _, segment := range segments {
		if _, ok := node.Children[segment]; !ok {
			node.Children[segment] = &SegmentNode{Children: make(map[string]*SegmentNode), SegmentName: segment}
		}
		node = node.Children[segment]
	}
	node.Config = config
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
	config := ua.FindConfigForPath(p)
	processedPath := ua.processSegments(node, p, config)
	return CollapseAdjacentDynamicIdentifiers(processedPath), nil
}

func (ua *PathAnalyzer) FindConfigForPath(path string) *CollapseConfig {
	node := ua.configRoot
	lastFoundConfig := ua.configRoot.Config

	segments := strings.Split(strings.Trim(path, "/"), "/")
	if segments[0] == "" {
		return lastFoundConfig
	}

	for _, segment := range segments {
		if nextNode, ok := node.Children[segment]; ok {
			node = nextNode
			if node.Config != nil {
				lastFoundConfig = node.Config
			}
		} else {
			// If we can't traverse further, the last config we found on the path is the most specific one.
			break
		}
	}
	return lastFoundConfig
}

func (ua *PathAnalyzer) processSegments(node *SegmentNode, p string, config *CollapseConfig) string {
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
		ua.updateNodeStats(currentNode, config)
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
	switch segment {
	case DynamicIdentifier:
		return ua.handleDynamicSegment(node)
	case "*":
		return ua.handleWildcardSegment(node)
	default:
		if node.IsNextDynamic() {
			if len(node.Children) > 1 {
				temp := node.Children[DynamicIdentifier]
				node.Children = map[string]*SegmentNode{}
				node.Children[DynamicIdentifier] = temp
			}
			return node.Children[DynamicIdentifier]
		} else if child, exists := node.Children[segment]; exists {
			return child
		}
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

func (ua *PathAnalyzer) handleWildcardSegment(node *SegmentNode) *SegmentNode {
	if wildcardChild, exists := node.Children["*"]; exists {
		return wildcardChild
	} else {
		return ua.createWildcardNode(node)
	}
}

func (ua *PathAnalyzer) createWildcardNode(node *SegmentNode) *SegmentNode {
	wildcardNode := &SegmentNode{
		SegmentName: "*",
		Count:       0, // for wildcards its not relevant how many counts it has, it collapes neighbors
		Children:    make(map[string]*SegmentNode),
	}

	// This function is called when a ⋯/⋯ structure is detected.
	// We copy the children of the second '⋯' (the grandchildren) to the new '*' node.
	ua.copyGrandchildren(node, wildcardNode)

	// Surgically replace the first dynamic node with the new wildcard node,
	// leaving other children of the parent node intact.
	delete(node.Children, DynamicIdentifier)
	node.Children["*"] = wildcardNode

	return wildcardNode
}

// copyGrandchildren finds the child and grandchild dynamic nodes and copies the grandchild's children to the destination node.
func (ua *PathAnalyzer) copyGrandchildren(src, dst *SegmentNode) {
	if child, exists := src.Children[DynamicIdentifier]; exists {
		if grandchild, exists := child.Children[DynamicIdentifier]; exists {
			shallowChildrenCopy(grandchild, dst)
		}
	}
}

func (ua *PathAnalyzer) updateNodeStats(node *SegmentNode, config *CollapseConfig) {
	switch {
	case node.Count > config.Threshold && !node.IsNextDynamic():
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

	case node.SegmentName == DynamicIdentifier && node.IsNextDynamic():
		// Second-level collapse: adjacent dynamic identifiers (⋯/⋯) -> wildcard (*)
		ua.createWildcardNode(node)
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

// so in this masterful logic: we have 3 types of nodes:  the regular ,the ellipsis and the wildcard
// if the path analyser is above the threshold it creates the ellipsis
// if two ellipsis are adjacent it creates the asterix (and currently messes up the node tree)
func CollapseAdjacentDynamicIdentifiers(p string) string {
	segments := strings.Split(p, "/")
	var result []string
	inDynamicSequence := false

	for i := 0; i < len(segments); i++ {
		isDynamic := segments[i] == DynamicIdentifier

		if isDynamic && !inDynamicSequence {
			// Check if this starts a sequence of at least two dynamic identifiers ## TODO: @constanze check if we ever have two asterix adjacent
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
