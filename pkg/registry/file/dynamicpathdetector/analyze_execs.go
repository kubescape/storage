package dynamicpathdetector

import (
	"slices"
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

// execArgsSep joins argv tokens for sorting and dedupe keys. It is the same
// unit-separator glyph ExecCalls.String uses, so it cannot appear in argv.
const execArgsSep = "␟"

// ExecAnalyzer carries the thresholds AnalyzeExecs applies per binary:
// threshold is the fallback, configs are the shared per-prefix overrides
// (the same CollapseConfigs opens use), matched against the exec path.
type ExecAnalyzer struct {
	threshold int
	configs   []CollapseConfig
}

// NewExecAnalyzer builds an ExecAnalyzer. A non-positive defaultThreshold
// means "use ExecDynamicThreshold" — a literal 0 would collapse every
// argument. configs is copied so the caller can reuse the slice.
func NewExecAnalyzer(defaultThreshold int, configs []CollapseConfig) *ExecAnalyzer {
	if defaultThreshold <= 0 {
		defaultThreshold = ExecDynamicThreshold
	}
	copied := make([]CollapseConfig, len(configs))
	copy(copied, configs)
	return &ExecAnalyzer{threshold: defaultThreshold, configs: copied}
}

// thresholdFor returns the collapse threshold for a binary path. Same
// longest-prefix-at-boundary, first-entry-wins-on-ties rule as
// PathAnalyzer.effectiveThreshold.
func (ea *ExecAnalyzer) thresholdFor(path string) int {
	bestLen := -1
	best := ea.threshold
	for i := range ea.configs {
		c := &ea.configs[i]
		if len(c.Prefix) > bestLen && c.Threshold >= 1 && hasPrefixAtBoundary(path, c.Prefix) {
			bestLen = len(c.Prefix)
			best = c.Threshold
		}
	}
	return best
}

// argNode is one argv position in a binary's argument trie.
type argNode struct {
	children map[string]*argNode
	terminal bool // an argv ends at this node
	envs     mapset.Set[string]
}

func newArgNode() *argNode {
	return &argNode{children: make(map[string]*argNode), envs: mapset.NewThreadUnsafeSet[string]()}
}

// AnalyzeExecs collapses high-variety exec arguments into the exec wildcards
// the runtime matcher understands (see CompareExecArgs):
//
//  1. Entries are grouped by Path. ArgsRequired entries and entries with no
//     Args pass through unchanged.
//  2. argv[0] stays literal unless the binary has more than threshold
//     distinct argv[0] values (typically an interpreter running many
//     scripts). Then argv[0] collapses by path shape, the way opens do
//     (/tmp/tmp.1/run.sh → /tmp/⋯/run.sh), and to a bare ⋯ if it is still
//     over threshold.
//  3. Each binary's argvs go into tries, one level per argv position, with a
//     separate trie per (argv[0], argc), so argvs of different lengths never
//     merge into each other. A node with more than threshold distinct
//     children — or with an existing ⋯ child — has its children merged into
//     a single ⋯ child, subtrees unioned.
//  4. Existing ⋯⋯ patterns are deduped by argv and every pattern absorbs the
//     literals it covers. If the binary still has more than threshold
//     distinct entries after that, it falls back to [argv0, ⋯⋯]: known
//     binary, any arguments.
//
// Entries containing ⋯ or ⋯⋯ are kept as patterns and absorb any literal
// entry they cover. Envs of merged entries are unioned. Output is sorted by path
// and argv, and AnalyzeExecs(AnalyzeExecs(x)) == AnalyzeExecs(x). The result
// is never nil: stored profiles encode empty execs as [], as they did with
// DeflateStringer.
func AnalyzeExecs(execs []types.ExecCalls, analyzer *ExecAnalyzer) []types.ExecCalls {
	if analyzer == nil {
		analyzer = NewExecAnalyzer(ExecDynamicThreshold, nil)
	}

	out := make([]types.ExecCalls, 0, len(execs))
	seenPassthrough := mapset.NewThreadUnsafeSet[string]()
	byPath := make(map[string][]types.ExecCalls)
	var paths []string
	for _, e := range execs {
		if e.ArgsRequired || len(e.Args) == 0 {
			if seenPassthrough.Add(e.String()) {
				out = append(out, e)
			}
			continue
		}
		if _, ok := byPath[e.Path]; !ok {
			paths = append(paths, e.Path)
		}
		byPath[e.Path] = append(byPath[e.Path], e)
	}

	for _, path := range paths {
		out = append(out, analyzeBinaryExecs(path, byPath[path], analyzer.thresholdFor(path))...)
	}

	slices.SortFunc(out, func(a, b types.ExecCalls) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		if c := strings.Compare(strings.Join(a.Args, execArgsSep), strings.Join(b.Args, execArgsSep)); c != 0 {
			return c
		}
		if a.ArgsRequired != b.ArgsRequired {
			if b.ArgsRequired {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.Join(a.Envs, execArgsSep), strings.Join(b.Envs, execArgsSep))
	})
	return out
}

// argRoot keys one argument trie: argvs only share a trie when they have the
// same argv[0] and the same length.
type argRoot struct {
	argv0 string
	argc  int
}

// analyzeBinaryExecs runs steps 2-4 of AnalyzeExecs for one binary.
func analyzeBinaryExecs(path string, execs []types.ExecCalls, threshold int) []types.ExecCalls {
	execs = collapseArgv0(execs, threshold)
	roots := make(map[argRoot]*argNode)
	var anyArgs []types.ExecCalls
	anyArgsIndex := make(map[string]int)
	for _, e := range execs {
		if slices.Contains(e.Args[1:], ExecArgsWildcard) {
			// Existing ⋯⋯ patterns bypass the trie, so dedupe them here by
			// argv, unioning envs, so each distinct pattern counts once.
			key := strings.Join(e.Args, execArgsSep)
			if i, ok := anyArgsIndex[key]; ok {
				anyArgs[i].Envs = sortedEnvs(mapset.NewThreadUnsafeSet(slices.Concat(anyArgs[i].Envs, e.Envs)...))
				continue
			}
			anyArgsIndex[key] = len(anyArgs)
			e.Envs = sortedEnvs(mapset.NewThreadUnsafeSet(e.Envs...))
			anyArgs = append(anyArgs, e)
			continue
		}
		key := argRoot{argv0: e.Args[0], argc: len(e.Args)}
		node, ok := roots[key]
		if !ok {
			node = newArgNode()
			roots[key] = node
		}
		for _, arg := range e.Args[1:] {
			child, ok := node.children[arg]
			if !ok {
				child = newArgNode()
				node.children[arg] = child
			}
			node = child
		}
		node.terminal = true
		node.envs.Append(e.Envs...)
	}

	var patterns []types.ExecCalls
	for key, root := range roots {
		collapseArgNode(root, threshold)
		emitArgPatterns(root, path, []string{key.argv0}, &patterns)
	}

	// The ceiling counts what remains after absorption: literals an existing
	// pattern already covers add no allowed behavior and must not broaden the
	// binary to [argv0, ⋯⋯].
	consolidated := absorbCoveredLiterals(append(anyArgs, patterns...))
	if len(consolidated) > threshold {
		return anyArgsPerArgv0(path, execs)
	}
	return consolidated
}

// collapseArgv0 rewrites argv[0] by path shape when a binary has more than
// threshold distinct argv[0] values, and to a bare ⋯ when that still leaves
// more than threshold. Every rewritten value is checked against the original
// with CompareExecArgs, so the result always matches its input; a path
// analyzer result that doesn't (e.g. the * its threshold-1 shortcut emits,
// which is a literal in exec args) falls back to ⋯. Input is not mutated.
func collapseArgv0(execs []types.ExecCalls, threshold int) []types.ExecCalls {
	distinct := mapset.NewThreadUnsafeSet[string]()
	for _, e := range execs {
		distinct.Add(e.Args[0])
	}
	if distinct.Cardinality() <= threshold {
		return execs
	}

	analyzer := NewPathAnalyzer(max(threshold, 2))
	argv0s := mapset.Sorted(distinct)
	for _, argv0 := range argv0s {
		_, _ = analyzer.AnalyzePath(argv0, "argv0")
	}
	mapped := make(map[string]string, len(argv0s))
	shapes := mapset.NewThreadUnsafeSet[string]()
	for _, argv0 := range argv0s {
		shape, err := analyzer.AnalyzePath(argv0, "argv0")
		switch {
		case err != nil || !strings.Contains(shape, DynamicIdentifier):
			shape = argv0 // unchanged apart from path.Clean: keep the original
		case !CompareExecArgs([]string{shape}, []string{argv0}):
			shape = DynamicIdentifier
		}
		mapped[argv0] = shape
		shapes.Add(shape)
	}
	if shapes.Cardinality() > threshold {
		for argv0 := range mapped {
			mapped[argv0] = DynamicIdentifier
		}
	}

	out := make([]types.ExecCalls, len(execs))
	for i, e := range execs {
		out[i] = e
		if shape := mapped[e.Args[0]]; shape != e.Args[0] {
			out[i].Args = slices.Clone(e.Args)
			out[i].Args[0] = shape
		}
	}
	return out
}

// collapseArgNode merges a node's children into a single ⋯ child when there
// are more than threshold of them, or when a ⋯ child already exists (so an
// earlier collapse keeps absorbing new literals), then recurses.
func collapseArgNode(node *argNode, threshold int) {
	_, hasDynamic := node.children[DynamicIdentifier]
	if len(node.children) > threshold || (hasDynamic && len(node.children) > 1) {
		merged := newArgNode()
		for _, child := range node.children {
			mergeArgNode(merged, child)
		}
		node.children = map[string]*argNode{DynamicIdentifier: merged}
	}
	for _, child := range node.children {
		collapseArgNode(child, threshold)
	}
}

// mergeArgNode unions src's subtree into dst.
func mergeArgNode(dst, src *argNode) {
	dst.terminal = dst.terminal || src.terminal
	dst.envs = dst.envs.Union(src.envs)
	for arg, child := range src.children {
		if existing, ok := dst.children[arg]; ok {
			mergeArgNode(existing, child)
		} else {
			dst.children[arg] = child
		}
	}
}

// emitArgPatterns appends one ExecCalls per terminal node under node.
func emitArgPatterns(node *argNode, path string, prefix []string, out *[]types.ExecCalls) {
	if node.terminal {
		*out = append(*out, types.ExecCalls{Path: path, Args: slices.Clone(prefix), Envs: sortedEnvs(node.envs)})
	}
	for arg, child := range node.children {
		emitArgPatterns(child, path, append(prefix, arg), out)
	}
}

// anyArgsPerArgv0 is the per-binary ceiling: one [argv0, ⋯⋯] entry per
// distinct argv0, carrying the union of that argv0's envs.
func anyArgsPerArgv0(path string, execs []types.ExecCalls) []types.ExecCalls {
	envs := make(map[string]mapset.Set[string])
	var argv0s []string
	for _, e := range execs {
		set, ok := envs[e.Args[0]]
		if !ok {
			set = mapset.NewThreadUnsafeSet[string]()
			envs[e.Args[0]] = set
			argv0s = append(argv0s, e.Args[0])
		}
		set.Append(e.Envs...)
	}
	out := make([]types.ExecCalls, 0, len(argv0s))
	for _, argv0 := range argv0s {
		out = append(out, types.ExecCalls{Path: path, Args: []string{argv0, ExecArgsWildcard}, Envs: sortedEnvs(envs[argv0])})
	}
	return out
}

// absorbCoveredLiterals drops any literal entry covered by a pattern entry
// (one containing ⋯ or ⋯⋯) of the same binary, per CompareExecArgs (the
// runtime matcher), merging its envs into the pattern. Mirrors
// consolidateOpens: patterns are always kept. Patterns are tried in sorted
// order so the pattern that receives the envs doesn't depend on input order.
func absorbCoveredLiterals(execs []types.ExecCalls) []types.ExecCalls {
	var patterns, literals []types.ExecCalls
	for _, e := range execs {
		if slices.ContainsFunc(e.Args, func(arg string) bool { return strings.Contains(arg, DynamicIdentifier) }) {
			patterns = append(patterns, e)
		} else {
			literals = append(literals, e)
		}
	}
	if len(patterns) == 0 {
		return literals
	}
	slices.SortFunc(patterns, func(a, b types.ExecCalls) int {
		return strings.Compare(strings.Join(a.Args, execArgsSep), strings.Join(b.Args, execArgsSep))
	})
	out := patterns
	for _, e := range literals {
		covered := false
		for i := range patterns {
			if CompareExecArgs(patterns[i].Args, e.Args) {
				out[i].Envs = sortedEnvs(mapset.NewThreadUnsafeSet(slices.Concat(out[i].Envs, e.Envs)...))
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, e)
		}
	}
	return out
}

func sortedEnvs(set mapset.Set[string]) []string {
	if set.Cardinality() == 0 {
		return nil
	}
	return mapset.Sorted(set)
}
