package dynamicpathdetector

import (
	"path"
	"strings"
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
//
// Tiebreak on equal-length prefixes: FIRST entry wins (strict `>`). This
// must mirror FindConfigForPath so callers using FindConfigForPath to
// introspect the active config see the same result the analyzer actually
// uses at walk time. Mismatched comparators (`>=` vs `>`) on duplicate
// prefixes are a silent footgun for anyone who doesn't dedupe configs.
func (ua *PathAnalyzer) effectiveThreshold(pathPrefix string) int {
	bestLen := -1
	best := ua.threshold
	for i := range ua.configs {
		c := &ua.configs[i]
		if len(c.Prefix) > bestLen && hasPrefixAtBoundary(pathPrefix, c.Prefix) {
			bestLen = len(c.Prefix)
			best = c.Threshold
		}
	}
	return best
}

// hasPrefixAtBoundary is like strings.HasPrefix but only matches if the
// prefix ends at a path boundary (either pathPrefix == prefix, or the next
// rune in pathPrefix is '/'). Prevents "/etc" matching "/etcd".
//
// Special case: prefix == "/" — the trailing '/' already implies a boundary,
// and any absolute path begins with '/'. Without this case, a user-supplied
// `{Prefix:"/", Threshold:X}` config would silently never match for any
// path past the root (e.g. "/foo" since pathPrefix[1] == 'f', not '/'),
// which means an explicit catch-all override could not actually override
// the analyzer's default threshold.
func hasPrefixAtBoundary(pathPrefix, prefix string) bool {
	// Empty-prefix guard. CodeRabbit upstream PR #323 finding #10:
	// without this, hasPrefixAtBoundary("/foo", "") falls through to
	// pathPrefix[0] == '/', which is true for any absolute path —
	// effectively treating `""` as a root-matching prefix. None of the
	// shipped configs use an empty prefix, but operators could supply
	// one via CollapseConfiguration CR, and an explicit guard makes
	// the invariant load-bearing rather than incidental.
	if prefix == "" {
		return true
	}
	// Normalise operator-supplied trailing slashes. A CollapseConfig with
	// `Prefix:"/etc/"` semantically means the same as `Prefix:"/etc"` —
	// the slash is the implicit segment boundary. Without this, an
	// operator authoring `/etc/` via CollapseConfiguration CR would
	// silently never match (the byte-equality check rejects `/etc/foo`
	// because `/etc/foo[:5]` = `/etc/` only when prefix is `/etc/`, which
	// then forces the next char to be at offset 5 — but offset 5 IS the
	// boundary slash itself; the boundary check expects another `/`
	// AFTER that, which is `f`, not `/` → false). Matthias upstream
	// PR #323 follow-up.
	for len(prefix) > 1 && prefix[len(prefix)-1] == '/' {
		prefix = prefix[:len(prefix)-1]
	}
	if len(pathPrefix) < len(prefix) {
		return false
	}
	if pathPrefix[:len(prefix)] != prefix {
		return false
	}
	if len(pathPrefix) == len(prefix) {
		return true
	}
	if prefix == "/" {
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
		// Two thresholds at two scopes — necessary because processSegment
		// and updateNodeStats ask different questions about different nodes:
		//
		// insertThreshold is for the PARENT node's config (path prefix up
		// to, but not including, the current segment). It answers: "if
		// we need to add `segment` under the parent, should we wildcard
		// the parent's children instead (threshold == 1)?". Using p[:i]
		// here would incorrectly apply the current segment's own config,
		// causing a {Prefix: "/instant", Threshold: 1} rule to wildcard
		// the "instant" segment itself and produce "/*/*/*" rather than
		// "/instant/*".
		//
		// collapseThreshold is for the CURRENT node's config (path prefix
		// INCLUDING the current segment, i.e. the node we just descended
		// to). It answers: "do this node's direct children exceed the
		// collapse threshold configured for this node's path?". Here we
		// do want p[:i] — updateNodeStats then collapses the current
		// node's children to ⋯ when Count > threshold.
		insertThreshold := ua.effectiveThreshold(p[:start])
		collapseThreshold := ua.effectiveThreshold(p[:i])
		currentNode = ua.processSegment(currentNode, segment, insertThreshold)
		ua.updateNodeStats(currentNode, collapseThreshold)
		buf = append(buf, currentNode.SegmentName...)
		// Wildcard absorbs the rest of the path: once a segment has been
		// emitted as `*`, walking deeper would just append more "/*"
		// suffixes, producing "/a/*/*/*" where the correct output is
		// "/a/*". Terminate emission here.
		if currentNode.SegmentName == WildcardIdentifier {
			break
		}
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
// WildcardIdentifier (*) child, absorbing any existing subtree counts into it.
// Used for the threshold-1 short-circuit: a CollapseConfig with Threshold == 1
// means "any new child segment under this prefix is noise", so the FIRST new
// segment immediately wildcards (there are typically no children to absorb on
// the first call; if the analyzer has previously seen children there, they
// get folded into the wildcard subtree at this point). The semantics are
// pinned by TestAnalyzeOpensThreshold1ImmediateWildcard /
// "single path - no collapse yet" which expects /instant/only-child/data
// to collapse to /instant/* after a single insert.
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

		// The absorbed children become dynamicChild's own children —
		// update dynamicChild.Count so subsequent updateNodeStats calls
		// on this node can correctly detect that the grandchild level
		// also exceeds its threshold and trigger the next collapse.
		// Without this, multi-level grids like /a/{many}/{many}/leaf
		// only collapse the first level and leave the grandchild
		// literals intact in the output.
		dynamicChild.Count = len(dynamicChild.Children)

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

// CompareDynamic checks whether `regularPath` is matched by `dynamicPath`.
// The dynamic path may contain DynamicIdentifier (⋯, exactly-one-segment
// wildcard) or WildcardIdentifier (*, zero-or-more-segment mid-path /
// one-or-more-segment trailing wildcard). The node-agent R0002 rule
// (Files Access Anomalies) uses this at every file-open to decide whether
// the access is in-profile.
//
// Anchoring contract:
//   - Anchored patterns (start with `/`): `/etc/*` matches files UNDER
//     /etc but NOT the bare `/etc` directory itself, mirroring shell
//     glob semantics. This avoids R0002 silently allowing access to a
//     profiled directory's parent.
//   - Unanchored `*` (no leading slash): explicit catch-all that also
//     matches the root path `/`. The only way to whitelist `/` itself
//     is an explicit unanchored `*`.
//
// Trailing-slash insensitivity: `/etc/` is treated as `/etc`, and
// `/etc/passwd/` as `/etc/passwd`. Trailing empty path components from
// `strings.Split` are trimmed so `len(regular) > 0` correctly reflects
// the presence of a real path tail when matching trailing `*`.
//
// The empty regular path (`""`) is treated as "no path" and matches
// nothing — distinct from the root path `/`, which matches unanchored
// `*` per the contract above.
func CompareDynamic(dynamicPath, regularPath string) bool {
	// Empty inputs match nothing. Note that splitPath("") and splitPath("/")
	// both yield [""] after trim, so without this guard an empty profile
	// entry would silently match the root path.
	if dynamicPath == "" || regularPath == "" {
		return false
	}
	// Dispatch by `*` count:
	//
	//   0 or 1 `*`  → zero-alloc index-based segment walk (compareSegmentsIndex).
	//                 This is the hot R0002 / file-open path; on `main` it
	//                 measured ~16 ns/op, 0 allocs/op. The previous splitPath +
	//                 slice-based descent moved it to ~85 ns/op, 128 B/op,
	//                 2 allocs/op — reverted here.
	//   2+ `*`      → splitPath + DP-memoised core. The memo absorbs the
	//                 exponential re-entry of multi-`*` patterns, which the
	//                 index-based walk would still hit. Allocation cost is
	//                 acceptable because multi-`*` patterns are rare and
	//                 author-supplied, not on the per-event hot path.
	//
	// Matthias's upstream PR #323 perf review drove this split.
	if countStarSegments(dynamicPath) >= 2 {
		// Multiple `*` segments: try collapsing consecutive runs first.
		// Spec §5.1 makes adjacent `*`s redundant (mid `*` is 0+
		// idempotent; trailing `*` is preserved by the run's last
		// element). After collapse, a 2+-`*` pattern like `/a/*/*/b`
		// reduces to a single-`*` `/a/*/b` and falls into the
		// zero-allocation linear path. Only TRULY non-adjacent
		// multi-`*` patterns (e.g. `/a/*/b/*/c`) still take the memo
		// path. The collapse is only invoked on the slow path; the
		// 0/1-`*` hot path pays no overhead.
		dynamicPath = collapseConsecutiveStars(dynamicPath)
		if countStarSegments(dynamicPath) >= 2 {
			dynamic := splitPath(dynamicPath)
			regular := splitPath(regularPath)
			memo := make(map[[2]int]bool, len(dynamic)*len(regular))
			return compareSegmentsMemo(dynamic, regular, 0, 0, memo)
		}
	}
	return compareSegmentsIndex(dynamicPath, 0, regularPath, 0)
}

// collapseConsecutiveStars replaces runs of consecutive `*` segments
// with a single `*`. `/a/*/*/b` → `/a/*/b`, `/*/*/*/x` → `/*/x`, etc.
//
// Semantic equivalence under v0.0.1 spec §5.1:
//
//   - Mid `*` matches 0+ segments; collapsing N consecutive mid `*`s
//     into one preserves the 0+ arity.
//   - Trailing `*` matches 1+ segments. A run of consecutive `*`s
//     ending in a trailing position still requires 1+ from the run's
//     last element; the upstream mid `*`s each contribute 0+. Net: 1+.
//   - Therefore `/x/*/*/* ` (trailing run) ≡ `/x/*` (single trailing `*`)
//     and `/x/*/*/y` (mid run) ≡ `/x/*/y` (single mid `*`).
//
// Two-pass implementation:
//
//  1. Zero-allocation scan for `/*/*` with a segment-boundary check on
//     the trailing `*`. If absent (the common case), return p as-is.
//  2. Build the collapsed string via strings.Builder (one allocation).
//
// The hot path — patterns with no adjacent `*` — pays only Pass 1's
// scan cost (~few ns) and no allocation.
//
// Producers' linter SHOULD flag adjacent `*` and have authors collapse
// in source. This matcher-side collapse is the safety net for legacy
// or hand-authored profiles.
func collapseConsecutiveStars(p string) string {
	// Pass 1: detect any "/*/*" with the second `*` actually a `*` segment.
	needsCollapse := false
	for i := 0; i+3 < len(p); i++ {
		if p[i] != '/' || p[i+1] != '*' || p[i+2] != '/' || p[i+3] != '*' {
			continue
		}
		// p[i+3] is `*`. It's a `*` SEGMENT iff p[i+4] is `/` or string-end.
		if i+4 == len(p) || p[i+4] == '/' {
			needsCollapse = true
			break
		}
	}
	if !needsCollapse {
		return p
	}

	// Pass 2: build collapsed string. We walk segments and drop any `*`
	// segment whose immediate predecessor was also `*`.
	var b strings.Builder
	b.Grow(len(p))
	// Track whether we just emitted a `*` segment, so we know to drop
	// subsequent `*` segments AND their preceding separator slash.
	prevSegWasStar := false
	// Track the position we're about to write a `/` from (so we can
	// drop it if the next segment turns out to be a collapsed `*`).
	pendingSlash := false
	i := 0
	for i < len(p) {
		// Emit any pending separator if the next byte starts a segment.
		// We DON'T emit a `/` if we're about to drop a `*` segment.
		if p[i] == '/' {
			pendingSlash = true
			i++
			continue
		}
		segStart := i
		for i < len(p) && p[i] != '/' {
			i++
		}
		seg := p[segStart:i]
		isStar := seg == "*"
		if isStar && prevSegWasStar {
			// Skip this `*` AND its leading slash (pendingSlash) —
			// they're absorbed by the previous `*`.
			pendingSlash = false
			continue
		}
		if pendingSlash {
			b.WriteByte('/')
			pendingSlash = false
		}
		b.WriteString(seg)
		prevSegWasStar = isStar
	}
	// Trailing slash, if any (the path ended in `/`).
	if pendingSlash {
		b.WriteByte('/')
	}
	return b.String()
}

// countStarSegments counts the number of standalone `*` segments in a
// path. A `*` segment is a single `*` byte bounded by `/` or string-edge
// — distinct from literal `*` characters embedded inside other tokens
// (which v0.0.1 does not currently distinguish but may via `\*` escaping
// in v0.0.2 per spec §5.1).
//
// Zero-allocation: scans the string in place.
func countStarSegments(p string) int {
	count := 0
	for i := 0; i < len(p); i++ {
		if p[i] != '*' {
			continue
		}
		leftOK := i == 0 || p[i-1] == '/'
		rightOK := i+1 == len(p) || p[i+1] == '/'
		if leftOK && rightOK {
			count++
		}
	}
	return count
}

// segAt returns the segment of `s` starting at byte offset `pos`,
// together with the byte index immediately after the segment's trailing
// `/` (or len(s) if the segment is the last one).
//
// Zero-allocation: returns a slice into the source string.
func segAt(s string, pos int) (seg string, nextPos int) {
	start := pos
	for pos < len(s) && s[pos] != '/' {
		pos++
	}
	seg = s[start:pos]
	if pos < len(s) {
		// skip trailing `/`
		return seg, pos + 1
	}
	return seg, pos
}

// compareSegmentsIndex is the zero-allocation core. It implements the
// same recursive-descent contract as the slice-based compareSegments
// but walks the source strings via byte indices, never splitting.
//
// di / ri are byte offsets into dynamicPath / regularPath respectively.
//
// Per the precondition in CompareDynamic, the dynamic path contains
// AT MOST one `*` segment. The function therefore never re-enters with
// a stale `*` position — backtracking depth is bounded by the number
// of segments in the regular path on the unique mid-`*` shape.
func compareSegmentsIndex(dynamicPath string, di int, regularPath string, ri int) bool {
	dl, rl := len(dynamicPath), len(regularPath)
	if di >= dl {
		return ri >= rl
	}
	dSeg, dNext := segAt(dynamicPath, di)
	if dSeg == WildcardIdentifier {
		// Trailing `*` matches one OR MORE remaining segments — never
		// zero. This is what makes `/etc/*` not match the bare `/etc`
		// directory, while still matching `/etc/passwd` and deeper.
		if dNext >= dl {
			return ri < rl
		}
		// Mid-path `*`: zero-or-more semantics. Try every offset
		// including ri itself (wildcard consumed zero segments).
		for rTry := ri; rTry <= rl; {
			if compareSegmentsIndex(dynamicPath, dNext, regularPath, rTry) {
				return true
			}
			// Advance rTry past one segment + its trailing `/`.
			for rTry < rl && regularPath[rTry] != '/' {
				rTry++
			}
			if rTry >= rl {
				return false
			}
			rTry++ // skip `/`
		}
		return false
	}
	if ri >= rl {
		return false
	}
	rSeg, rNext := segAt(regularPath, ri)
	if dSeg == DynamicIdentifier || dSeg == rSeg {
		return compareSegmentsIndex(dynamicPath, dNext, regularPath, rNext)
	}
	return false
}

// compareSegmentsMemo is the DP-memoised core, reached only when the
// dynamic pattern has two or more `*` segments. It walks (di, ri) cursor
// pairs over the dynamic and regular slices. The semantics are identical
// to the index-based compareSegmentsIndex: only the redundant re-entry
// is eliminated.
//
// Per-state outcomes are cached in memo. On a cache hit the prior
// boolean is returned directly; on a miss the recursive expansion runs
// and the result is stored before return.
func compareSegmentsMemo(dynamic, regular []string, di, ri int, memo map[[2]int]bool) bool {
	if di == len(dynamic) {
		return ri == len(regular)
	}
	key := [2]int{di, ri}
	if v, ok := memo[key]; ok {
		return v
	}

	var result bool
	if dynamic[di] == WildcardIdentifier {
		// Trailing `*` matches one OR MORE remaining segments — never
		// zero. Identical semantics to the un-memoised form.
		if di == len(dynamic)-1 {
			result = ri < len(regular)
		} else {
			// Mid-path `*`: zero-or-more semantics. Try every offset
			// including i == 0 (wildcard consumed zero segments). The
			// memoisation cache absorbs the repeated work this loop
			// would otherwise inflict on re-entrant states.
			for i := ri; i <= len(regular); i++ {
				if compareSegmentsMemo(dynamic, regular, di+1, i, memo) {
					result = true
					break
				}
			}
		}
	} else if ri == len(regular) {
		result = false
	} else if dynamic[di] == DynamicIdentifier || dynamic[di] == regular[ri] {
		result = compareSegmentsMemo(dynamic, regular, di+1, ri+1, memo)
	}

	memo[key] = result
	return result
}

// splitPath splits a path on `/` and trims trailing empty segments
// produced by trailing slashes (e.g. `/etc/` -> ["", "etc"] not
// ["", "etc", ""]). The leading empty segment from a leading slash is
// preserved as the anchor marker. Single-element results are not
// trimmed so the root path `/` retains its `[""]` shape.
func splitPath(p string) []string {
	s := strings.Split(p, "/")
	for len(s) > 1 && s[len(s)-1] == "" {
		s = s[:len(s)-1]
	}
	return s
}

// FindConfigForPath returns a value copy of the CollapseConfig whose
// Prefix matches `path` with the longest match. Falls back to the
// analyzer's default config (Prefix:"/") when no per-prefix override
// applies, so the result is always meaningful — there is no "no match"
// signal.
//
// Returning by value keeps the analyzer's internal state immutable
// from callers. NewPathAnalyzerWithConfigs already makes a defensive
// inbound copy of `configs`; this is its outbound twin. Without it,
// `cfg := analyzer.FindConfigForPath(p); cfg.Threshold = 1` would
// silently mutate the analyzer's threshold map for every future call.
func (ua *PathAnalyzer) FindConfigForPath(path string) CollapseConfig {
	bestIdx := -1
	bestLen := -1
	for i := range ua.configs {
		cfg := &ua.configs[i]
		if hasPrefixAtBoundary(path, cfg.Prefix) && len(cfg.Prefix) > bestLen {
			bestIdx = i
			bestLen = len(cfg.Prefix)
		}
	}
	if bestIdx == -1 {
		return ua.defaultCfg
	}
	return ua.configs[bestIdx]
}

// CollapseAdjacentDynamicIdentifiers replaces runs of adjacent
// DynamicIdentifier segments (e.g. "/a/⋯/⋯/b") with a single
// WildcardIdentifier ("/a/*/b"). Static segments between dynamic
// identifiers prevent collapsing. String wrapper over the internal
// byte-level collapseAdjacentDynamic, intended for test coverage.
func CollapseAdjacentDynamicIdentifiers(p string) string {
	return string(collapseAdjacentDynamic([]byte(p)))
}
