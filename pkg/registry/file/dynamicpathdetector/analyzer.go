package dynamicpathdetector

import (
	"path"
	"sync"
)

// bufPool reuses byte-slice capacity across AnalyzePath calls. strings.Builder
// was tempting but its Reset() discards the buffer, defeating the pool; a raw
// []byte with len=0/cap-preserved survives reuse. Steady-state per-call cost
// is one string allocation (the final string conversion) and nothing else.
// Thread-safe by virtue of sync.Pool.
const defaultBuildBufCap = 128

var bufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, defaultBuildBufCap)
		return &b
	},
}

// NewPathAnalyzer builds an analyzer with a single global collapse threshold
// and no per-prefix overrides — equivalent behaviour to the pre-CollapseConfig
// world. Retained so existing callers don't need to change.
func NewPathAnalyzer(threshold int) *PathAnalyzer {
	return NewPathAnalyzerWithConfigs(threshold, nil)
}

// NewPathAnalyzerWithConfigs builds an analyzer whose collapse threshold can
// vary per path prefix. defaultThreshold applies when no CollapseConfig in
// configs matches; configs are checked longest-prefix-wins at walk time.
//
// configs is copied so the caller can reuse or mutate the slice without
// affecting the analyzer.
func NewPathAnalyzerWithConfigs(defaultThreshold int, configs []CollapseConfig) *PathAnalyzer {
	copied := make([]CollapseConfig, len(configs))
	copy(copied, configs)
	return &PathAnalyzer{
		RootNodes:  make(map[string]*SegmentNode),
		threshold:  defaultThreshold,
		configs:    copied,
		defaultCfg: CollapseConfig{Prefix: "/", Threshold: defaultThreshold},
	}
}

// effectiveThreshold returns the collapse threshold applicable to the given
// path prefix, picking the longest matching CollapseConfig or falling back
// to the analyzer's default. Loop is O(len(configs)) and configs is small
// (five entries in practice); no allocations.
func (ua *PathAnalyzer) effectiveThreshold(pathPrefix string) int {
	bestLen := 0
	best := ua.threshold
	for i := range ua.configs {
		c := &ua.configs[i]
		if len(c.Prefix) >= bestLen && hasPrefixAtBoundary(pathPrefix, c.Prefix) {
			bestLen = len(c.Prefix)
			best = c.Threshold
		}
	}
	return best
}

// hasPrefixAtBoundary is like strings.HasPrefix but only matches if the
// prefix ends at a path boundary (either pathPrefix == prefix, or the next
// rune in pathPrefix is '/'). Prevents "/etc" matching "/etcd".
func hasPrefixAtBoundary(pathPrefix, prefix string) bool {
	if len(pathPrefix) < len(prefix) {
		return false
	}
	if pathPrefix[:len(prefix)] != prefix {
		return false
	}
	if len(pathPrefix) == len(prefix) {
		return true
	}
	return pathPrefix[len(prefix)] == '/'
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
	return ua.processSegments(node, p), nil
}

func (ua *PathAnalyzer) processSegments(node *SegmentNode, p string) string {
	// Acquire a pooled byte-slice. len=0, cap preserved from previous reuse.
	bufPtr := bufPool.Get().(*[]byte)
	buf := (*bufPtr)[:0]
	if cap(buf) < len(p) {
		// Pooled capacity is too small for this input; grow once.
		buf = make([]byte, 0, len(p)+16)
	}

	currentNode := node
	i := 0
	for {
		start := i
		for i < len(p) && p[i] != '/' {
			i++
		}
		segment := p[start:i]
		// Effective threshold at this depth (allocation-free slice).
		threshold := ua.effectiveThreshold(p[:i])
		currentNode = ua.processSegment(currentNode, segment, threshold)
		ua.updateNodeStats(currentNode, threshold)
		buf = append(buf, currentNode.SegmentName...)
		i++
		if len(p) < i {
			break
		}
		buf = append(buf, '/')
	}

	// Post-process: collapse runs of adjacent DynamicIdentifier segments
	// (e.g. "/a/⋯/⋯/b") into a single WildcardIdentifier ("/a/*/b"). Done
	// in place by shrinking buf — zero allocation because the output is
	// always shorter than the input.
	buf = collapseAdjacentDynamic(buf)

	// string(buf) always copies, so it is safe to return the pool capacity
	// immediately afterwards — the returned string does not alias buf.
	out := string(buf)
	*bufPtr = buf
	bufPool.Put(bufPtr)
	return out
}

// collapseAdjacentDynamic compacts buf in place: any run of
// "⋯/⋯[/⋯…]" becomes a single "*". Returns a buf[:n] slice where n is
// the compacted length. Does not allocate; suitable for the hot path.
func collapseAdjacentDynamic(buf []byte) []byte {
	// DynamicIdentifier is U+22EF, three UTF-8 bytes: 0xE2 0x8B 0xAF.
	const d0, d1, d2 = 0xE2, 0x8B, 0xAF
	const dynLen = 3
	isDyn := func(i int) bool {
		return i+dynLen <= len(buf) && buf[i] == d0 && buf[i+1] == d1 && buf[i+2] == d2
	}

	out := 0
	i := 0
	for i < len(buf) {
		// Need at least "⋯/⋯" (7 bytes) to trigger a collapse.
		if isDyn(i) && i+dynLen+1+dynLen <= len(buf) && buf[i+dynLen] == '/' && isDyn(i+dynLen+1) {
			buf[out] = '*'
			out++
			// Consume "⋯/⋯" plus any further "/⋯" in the run.
			i += dynLen + 1 + dynLen
			for i+1+dynLen <= len(buf) && buf[i] == '/' && isDyn(i+1) {
				i += 1 + dynLen
			}
			continue
		}
		buf[out] = buf[i]
		out++
		i++
	}
	return buf[:out]
}

func (ua *PathAnalyzer) processSegment(node *SegmentNode, segment string, threshold int) *SegmentNode {
	if segment == DynamicIdentifier {
		return ua.handleDynamicSegment(node)
	}
	// Wildcard short-circuit: once a node has a * child, all paths through
	// it go there. This is the glob-style "collapse everything below here"
	// behaviour; set up either by threshold=1 (see below) or by a caller
	// explicitly feeding a WildcardIdentifier segment.
	if wildcardChild, exists := node.Children[WildcardIdentifier]; exists {
		return wildcardChild
	}
	if node.IsNextDynamic() {
		if len(node.Children) > 1 {
			temp := node.Children[DynamicIdentifier]
			node.Children = map[string]*SegmentNode{}
			node.Children[DynamicIdentifier] = temp
		}
		return node.Children[DynamicIdentifier]
	}
	if child, exists := node.Children[segment]; exists {
		return child
	}
	// Threshold-1 short-circuit: a prefix explicitly configured to accept
	// one unique child (CollapseConfig Threshold == 1) collapses to * on
	// the first *new* segment rather than going through the ⋯ path. This
	// matches the caller's intent of "anything under /app is noise".
	if threshold == 1 {
		return ua.createWildcardNode(node)
	}
	return ua.handleNewSegment(node, segment)
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

// createWildcardNode replaces all of node's existing children with a single
// WildcardIdentifier (*) child, absorbing the existing subtree counts into it.
// Used for the threshold-1 short-circuit: once a prefix is configured to keep
// at most one unique child, any second unique value collapses the whole
// subtree to *.
func (ua *PathAnalyzer) createWildcardNode(node *SegmentNode) *SegmentNode {
	wildcard := &SegmentNode{
		SegmentName: WildcardIdentifier,
		Count:       0,
		Children:    make(map[string]*SegmentNode),
	}
	// Absorb any previously-accumulated children. Mirrors createDynamicNode.
	for _, child := range node.Children {
		shallowChildrenCopy(child, wildcard)
	}
	node.Children = map[string]*SegmentNode{
		WildcardIdentifier: wildcard,
	}
	return wildcard
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

// updateNodeStats collapses node's children into a single ⋯ (DynamicIdentifier)
// child once the number of distinct children exceeds the provided threshold.
// Threshold is passed in by the caller so per-prefix overrides (via
// CollapseConfig) can take effect without this function knowing about them.
func (ua *PathAnalyzer) updateNodeStats(node *SegmentNode, threshold int) {
	if node.Count > threshold && !node.IsNextDynamic() {
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

func CompareDynamic(dynamicPath, regularPath string) bool {
	dynamicIndex, regularIndex := 0, 0
	dynamicLen, regularLen := len(dynamicPath), len(regularPath)

	for dynamicIndex < dynamicLen && regularIndex < regularLen {
		// Find the next segment in dynamicPath
		dynamicSegmentStart := dynamicIndex
		for dynamicIndex < dynamicLen && dynamicPath[dynamicIndex] != '/' {
			dynamicIndex++
		}
		dynamicSegment := dynamicPath[dynamicSegmentStart:dynamicIndex]

		// Find the next segment in regularPath
		regularSegmentStart := regularIndex
		for regularIndex < regularLen && regularPath[regularIndex] != '/' {
			regularIndex++
		}
		regularSegment := regularPath[regularSegmentStart:regularIndex]

		if dynamicSegment != DynamicIdentifier && dynamicSegment != regularSegment {
			return false
		}

		// Move to the next segment
		dynamicIndex++
		regularIndex++
	}

	return dynamicIndex >= dynamicLen && regularIndex >= regularLen
}
