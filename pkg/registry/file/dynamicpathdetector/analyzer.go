package dynamicpathdetector

import (
	"strings"
)

const (
	WildcardIdentifier = "*"
)

var CollapseConfigs = []CollapseConfig{
	{Prefix: "/etc", Threshold: 50},
	{Prefix: "/opt", Threshold: 5},
	{Prefix: "/var/run", Threshold: 3},
	{Prefix: "/app", Threshold: 1},
}

var DefaultCollapseConfig = CollapseConfig{
	Prefix:    "/",
	Threshold: 5,
}

func NewPathAnalyzer(threshold int) *PathAnalyzer {
	return newAnalyzer(CollapseConfig{Prefix: "/", Threshold: threshold}, CollapseConfigs)
}

func NewPathAnalyzerWithConfigs(configs []CollapseConfig) *PathAnalyzer {
	return newAnalyzer(DefaultCollapseConfig, configs)
}

func newAnalyzer(defaultCfg CollapseConfig, configs []CollapseConfig) *PathAnalyzer {
	matcher := &PathAnalyzer{
		root:       NewTrieNode(),
		identRoots: make(map[string]*TrieNode),
		configs:    make([]CollapseConfig, len(configs)),
		defaultCfg: defaultCfg,
	}
	copy(matcher.configs, configs)
	applyConfigsToNode(matcher.root, &matcher.defaultCfg, matcher.configs)
	return matcher
}

func applyConfigsToNode(node *TrieNode, defaultCfg *CollapseConfig, configs []CollapseConfig) {
	addConfigToNode(node, defaultCfg)
	for i := range configs {
		addConfigToNode(node, &configs[i])
	}
}

func addConfigToNode(root *TrieNode, config *CollapseConfig) {
	node := root
	segments := strings.Split(strings.Trim(config.Prefix, "/"), "/")
	if segments[0] == "" {
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

func (pm *PathAnalyzer) getRoot(identifier string) *TrieNode {
	if root, ok := pm.identRoots[identifier]; ok {
		return root
	}
	newRoot := NewTrieNode()
	pm.identRoots[identifier] = newRoot
	return newRoot
}

// splitPath splits a path into non-empty segments.
func splitPath(path string) []string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	var result []string
	for _, p := range parts {
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func (pm *PathAnalyzer) AddPath(path string) {
	pm.addPathToRoot(pm.root, path)
}

func (pm *PathAnalyzer) addPathToRoot(root *TrieNode, path string) {
	parent := root

	segments := splitPath(path)
	if len(segments) == 0 {
		return
	}

	// Use pm.root as config trie for per-prefix threshold lookup.
	// Config advances AFTER navigation so threshold applies at the correct level.
	configNode := pm.root
	currentConfig := &pm.defaultCfg
	if configNode != nil && configNode.Config != nil {
		currentConfig = configNode.Config
	}

	for _, segment := range segments {
		// If a wildcard exists, it consumes the rest of the path.
		if wildcardNode, ok := parent.Children[WildcardIdentifier]; ok {
			wildcardNode.Count++
			return
		}

		// If a dynamic node exists, absorb this segment and continue.
		if dynamicNode, ok := parent.Children[DynamicIdentifier]; ok {
			parent = dynamicNode
			parent.Count++
			// Advance config after navigation
			if configNode != nil {
				if next, ok := configNode.Children[segment]; ok {
					configNode = next
					if configNode.Config != nil {
						currentConfig = configNode.Config
					}
				}
			}
			continue
		}

		// Handle DynamicIdentifier segment from input: merge siblings into new ⋯ node
		if segment == DynamicIdentifier {
			if _, exists := parent.Children[DynamicIdentifier]; !exists {
				dynamicNode := NewTrieNode()
				for _, child := range parent.Children {
					dynamicNode.Count += child.Count
					shallowChildrenCopy(child, dynamicNode)
				}
				parent.Children = map[string]*TrieNode{DynamicIdentifier: dynamicNode}
			}
			parent = parent.Children[DynamicIdentifier]
			parent.Count++
			// Advance config after navigation
			if configNode != nil {
				if next, ok := configNode.Children[segment]; ok {
					configNode = next
					if configNode.Config != nil {
						currentConfig = configNode.Config
					}
				}
			}
			continue
		}

		// Add new node if it doesn't exist
		child, exists := parent.Children[segment]
		if !exists {
			child = NewTrieNode()
			parent.Children[segment] = child
		}
		child.Count++

		// Special case: threshold of 1 immediately creates a wildcard
		if currentConfig != nil && currentConfig.Threshold == 1 && parent.Children[WildcardIdentifier] == nil {
			pm.createWildcardNode(parent)
			parent.Children[WildcardIdentifier].Count++
			return
		}

		// Standard collapse: if unique children > threshold, collapse to dynamic node
		if currentConfig != nil && len(parent.Children) > currentConfig.Threshold && parent.Children[DynamicIdentifier] == nil {
			pm.createDynamicNode(parent)
		}

		// After a potential collapse, find the correct child to traverse to next.
		if nextNode, ok := parent.Children[DynamicIdentifier]; ok {
			parent = nextNode
		} else if nextNode, ok := parent.Children[segment]; ok {
			parent = nextNode
		} else if _, ok := parent.Children[WildcardIdentifier]; ok {
			return
		} else {
			return
		}

		// Advance config AFTER navigation so threshold applies at the correct level
		if configNode != nil {
			if next, ok := configNode.Children[segment]; ok {
				configNode = next
				if configNode.Config != nil {
					currentConfig = configNode.Config
				}
			}
		}
	}
}

func shallowChildrenCopy(src, dst *TrieNode) {
	for key, srcChild := range src.Children {
		if dstChild, ok := dst.Children[key]; !ok {
			dst.Children[key] = srcChild
		} else {
			dstChild.Count += srcChild.Count
			shallowChildrenCopy(srcChild, dstChild)
		}
	}
}

func (pm *PathAnalyzer) createDynamicNode(node *TrieNode) {
	dynamicNode := NewTrieNode()
	for _, child := range node.Children {
		dynamicNode.Count += child.Count
		shallowChildrenCopy(child, dynamicNode)
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
	segments := splitPath(path)
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

func (pm *PathAnalyzer) collectPaths(node *TrieNode, currentPath string, paths *[]string) {
	if len(node.Children) == 0 {
		if currentPath != "" {
			*paths = append(*paths, currentPath)
		}
		return
	}
	for segment, child := range node.Children {
		newPath := currentPath + "/" + segment
		pm.collectPaths(child, newPath, paths)
	}
}

func (pm *PathAnalyzer) AnalyzePath(path string, identifier string) (string, error) {
	cleanPath := strings.Trim(path, "/")
	if cleanPath == "" {
		return "/", nil
	}

	root := pm.getRoot(identifier)

	segments := splitPath(cleanPath)
	if len(segments) == 0 {
		return "/", nil
	}

	// Read the tree state BEFORE adding the new path.
	// This ensures the current path doesn't see its own collapse.
	node := root
	var pathSegments []string

	for _, segment := range segments {
		if nextNode, ok := node.Children[WildcardIdentifier]; ok {
			node = nextNode
			pathSegments = append(pathSegments, WildcardIdentifier)
			break
		}
		if nextNode, ok := node.Children[DynamicIdentifier]; ok {
			node = nextNode
			pathSegments = append(pathSegments, DynamicIdentifier)
		} else if nextNode, ok := node.Children[segment]; ok {
			node = nextNode
			pathSegments = append(pathSegments, segment)
		} else {
			pathSegments = append(pathSegments, segment)
		}
	}

	// Now add the path to the tree (for future calls).
	pm.addPathToRoot(root, cleanPath)

	finalPath := "/" + strings.Join(pathSegments, "/")
	return CollapseAdjacentDynamicIdentifiers(finalPath), nil
}

// CollapseAdjacentDynamicIdentifiers replaces sequences of truly adjacent dynamic identifiers with a wildcard.
// Only consecutive ⋯/⋯ segments are collapsed to *. Static segments between ⋯ prevent collapsing.
func CollapseAdjacentDynamicIdentifiers(p string) string {
	segments := strings.Split(p, "/")
	var result []string
	i := 0
	for i < len(segments) {
		if segments[i] == DynamicIdentifier && i+1 < len(segments) && segments[i+1] == DynamicIdentifier {
			// Replace sequence of adjacent ⋯ with *
			result = append(result, WildcardIdentifier)
			for i < len(segments) && segments[i] == DynamicIdentifier {
				i++
			}
			continue
		}
		result = append(result, segments[i])
		i++
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
	if dynamic[0] == WildcardIdentifier {
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
