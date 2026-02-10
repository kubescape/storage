package dynamicpathdetector

import (
	"strings"
)

// This function builds a tree of nodes
const (
	WildcardIdentifier = "*"
)

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
var DefaultCollapseConfig = CollapseConfig{
	Prefix:    "/",
	Threshold: 50, //later set to 50
}

// NewPathAnalyzer is the primary constructor for the PathAnalyzer.
// It initializes the analyzer with a default set of collapse configurations
// and sets the global default threshold.
func NewPathAnalyzer(threshold int) *PathAnalyzer {
	DefaultCollapseConfig.Threshold = threshold
	return NewPathAnalyzerWithConfigs(CollapseConfigs)
}

// NewPathAnalyzerWithConfigs creates a PathAnalyzer with a specific set of collapse configurations.
func NewPathAnalyzerWithConfigs(configs []CollapseConfig) *PathAnalyzer {

	matcher := &PathAnalyzer{
		root: NewTrieNode(),
	}
	matcher.addConfig(&DefaultCollapseConfig)
	for i := range configs {
		matcher.addConfig(&configs[i])
	}
	return matcher
}

func (pm *PathAnalyzer) addConfig(config *CollapseConfig) {
	node := pm.root
	segments := strings.Split(strings.Trim(config.Prefix, "/"), "/")
	if segments[0] == "" { // Handle root prefix "/"
		node.Config = config
		return
	}
	for _, segment := range segments {
		if _, ok := node.Children[segment]; !ok {
			node.Children[segment] = NewTrieNode()
		}
		node = node.Children[segment]
	}
	node.Config = config
}

func (pm *PathAnalyzer) AddPath(path string) {
	parent := pm.root
	currentConfig := pm.root.Config

	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) == 0 || segments[0] == "" {
		return // Nothing to add for root path
	}

	for _, segment := range segments {
		// If a wildcard exists, it consumes the rest of the path.
		if wildcardNode, ok := parent.Children[WildcardIdentifier]; ok {
			wildcardNode.Count++
			return
		}

		// Check for second-level collapse (⋯/⋯ -> *)
		// This happens if the parent is a dynamic node and we are about to create another one.
		if parent.Children[DynamicIdentifier] != nil {
			// If the dynamic child itself has too many children, it will collapse.
			// This logic is complex. A simpler approach is to check after traversal.
		}

		// If a dynamic node exists, traverse it.
		if dynamicNode, ok := parent.Children[DynamicIdentifier]; ok {
			parent = dynamicNode
			if parent.Config != nil {
				currentConfig = parent.Config
			}
			// We still need to process the current segment under this dynamic node.
			// Let's adjust the logic to handle adding the segment to the dynamic node's children.
		} else {
			// Standard path traversal and creation
		}

		// --- Add new node if it doesn't exist ---
		child, exists := parent.Children[segment]
		if !exists {
			child = NewTrieNode()
			parent.Children[segment] = child
		}
		child.Count++

		// --- Check for collapse at the PARENT level ---
		// Special case: threshold of 1 immediately creates a wildcard
		if currentConfig.Threshold == 1 && parent.Children[WildcardIdentifier] == nil {
			pm.createWildcardNode(parent)
			parent.Children[WildcardIdentifier].Count++
			return // Path is consumed by the new wildcard
		}

		// Standard collapse: if children > threshold, collapse to dynamic node
		if len(parent.Children) > currentConfig.Threshold && parent.Children[DynamicIdentifier] == nil {
			pm.createDynamicNode(parent)
		}

		// After a potential collapse, find the correct child to traverse to next.
		if nextNode, ok := parent.Children[DynamicIdentifier]; ok {
			// The segment is now part of the dynamic node's logic, but we traverse into the dynamic node itself.
			parent = nextNode
		} else if nextNode, ok := parent.Children[segment]; ok {
			parent = nextNode
		} else if nextNode, ok := parent.Children[WildcardIdentifier]; ok {
			// This case is handled at the top of the loop.
			parent = nextNode
			return
		} else {
			// This should not be reached if logic is correct.
			// print error
			return
		}

		// Update config for the next level
		if parent.Config != nil {
			currentConfig = parent.Config
		}

		// Check for ⋯/⋯ -> * collapse
		// This checks if the current node is dynamic and its only child is also dynamic.
		if len(parent.Children) == 1 {
			if grandChild, isDynamic := parent.Children[DynamicIdentifier]; isDynamic {
				pm.createWildcardNode(parent)
				//print grandChild
				grandChild.Count++
				return
			}
		}
	}
}

func (pm *PathAnalyzer) createDynamicNode(node *TrieNode) {
	dynamicNode := NewTrieNode()
	dynamicNode.Config = node.Config // Inherit config
	for _, child := range node.Children {
		// A simple merge for demonstration. A real implementation might need deeper merging.
		dynamicNode.Count += child.Count
	}
	node.Children = map[string]*TrieNode{DynamicIdentifier: dynamicNode}
}

func (pm *PathAnalyzer) createWildcardNode(node *TrieNode) {
	wildcardNode := NewTrieNode()
	for _, child := range node.Children {
		wildcardNode.Count += child.Count
	}
	node.Children = map[string]*TrieNode{WildcardIdentifier: wildcardNode}
}

func (pm *PathAnalyzer) FindConfigForPath(path string) *CollapseConfig {
	node := pm.root
	var lastFoundConfig *CollapseConfig
	if node.Config != nil {
		lastFoundConfig = node.Config
	}

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
			break
		}
	}
	return lastFoundConfig
}

func (pm *PathAnalyzer) GetStoredPaths() []string {
	var storedPaths []string
	pm.collectPaths(pm.root, "", &storedPaths)
	return storedPaths
}

// collectPaths is a recursive helper to traverse the tree and build path strings.
func (pm *PathAnalyzer) collectPaths(node *TrieNode, currentPath string, paths *[]string) {
	// If it's a leaf node, we've found a full path.
	if len(node.Children) == 0 {
		if currentPath != "" {
			*paths = append(*paths, currentPath)
		}
		return
	}

	// Otherwise, continue traversing for each child.
	for segment, child := range node.Children {
		newPath := currentPath + "/" + segment
		pm.collectPaths(child, newPath, paths)
	}
}
