package dynamicpathdetector

import (
	"path"
	"regexp"
	"strings"
	"sync"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
)

func NewPathAnalyzer(threshold int) *PathAnalyzer {
	return &PathAnalyzer{
		RootNodes: make(map[string]*SegmentNode),
		threshold: threshold,
	}
}

var (
	regexCache = make(map[string]*regexp.Regexp)
	cacheMutex = &sync.RWMutex{}
)

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
	return ua.processSegments(node, p), nil
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

// This may have terrible performance penalties DO NOT MERGE
// Match checks if a path matches a pattern containing wildcards.
// It converts the pattern to a regular expression and performs the match.
// The supported wildcards are:
// - `*` (asterisk): matches any sequence of zero or more characters, including '/'.
// - `...` (ellipsis): matches any sequence of one or more characters, excluding '/'.
func Match(pattern, path string) (bool, error) {
	cacheMutex.RLock()
	re, found := regexCache[pattern]
	cacheMutex.RUnlock()

	if !found {
		var err error
		// Upgrade lock for writing
		cacheMutex.Lock()
		// Double-check in case it was compiled while waiting for the lock.
		if re, found = regexCache[pattern]; !found {
			// Convert pattern to regex string
			regexStr := regexp.QuoteMeta(pattern)
			// Replace our wildcards with their regex equivalents.
			// The ellipsis `...` becomes `\.\.\.` after quoting.
			regexStr = strings.ReplaceAll(regexStr, `\.\.\.`, `[^/]+`)
			// The asterisk `*` becomes `\*` after quoting.
			regexStr = strings.ReplaceAll(regexStr, `\*`, `.*`)

			// Anchor the regex to match the entire string
			re, err = regexp.Compile("^" + regexStr + "$")
			if err == nil {
				regexCache[pattern] = re
			}
		}
		cacheMutex.Unlock()

		if err != nil {
			return false, err
		}
	}

	return re.MatchString(path), nil
}

func CompareDynamic(dynamicPath, regularPath string) bool {
	// If the dynamic path contains no wildcards, perform a simple string comparison.
	if !strings.ContainsAny(dynamicPath, "*"+DynamicIdentifier) {

		logger.L().Debug("CompareDynamic: no wildcards, using simple string comparison",
			helpers.String("dynamicPath", dynamicPath),
			helpers.String("regularPath", regularPath))
		return dynamicPath == regularPath
	}

	// Otherwise, use the more powerful regex-based matching.
	logger.L().Debug("CompareDynamic: wildcards detected, using regex matching",
		helpers.String("pattern", dynamicPath),
		helpers.String("path", regularPath))

	matched, err := Match(dynamicPath, regularPath)
	if err != nil {
		// If the pattern is invalid, it cannot match.
		logger.L().Error("CompareDynamic: regex match failed with an error",
			helpers.String("pattern", dynamicPath),
			helpers.String("path", regularPath),
			helpers.Error(err))
		return false
	}
	logger.L().Debug("CompareDynamic: regex match result",
		helpers.String("pattern", dynamicPath),
		helpers.String("path", regularPath))
	return matched
}
