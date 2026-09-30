package dynamicpathdetector

import (
	"slices"
	"strconv"
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

// tokensKey is an injective encoding of a token list (length-prefixed), for
// map keys. A plain strings.Join is not injective: any separator glyph is
// valid argv data.
func tokensKey(tokens []string) string {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteString(strconv.Itoa(len(t)))
		b.WriteByte(':')
		b.WriteString(t)
	}
	return b.String()
}

// execKey identifies an ExecCalls entry exactly (path, argv, envs and
// ArgsRequired), injectively.
func execKey(e types.ExecCalls) string {
	return tokensKey([]string{e.Path, tokensKey(e.Args), tokensKey(e.Envs), strconv.FormatBool(e.ArgsRequired)})
}

// compareExecCalls is a total order: path, argv, ArgsRequired (false first),
// then envs. Slices compare element-wise, so distinct argvs never tie.
func compareExecCalls(a, b types.ExecCalls) int {
	if c := strings.Compare(a.Path, b.Path); c != 0 {
		return c
	}
	if c := slices.Compare(a.Args, b.Args); c != 0 {
		return c
	}
	if a.ArgsRequired != b.ArgsRequired {
		if b.ArgsRequired {
			return -1
		}
		return 1
	}
	return slices.Compare(a.Envs, b.Envs)
}

// hasDynamicToken reports whether args contain ⋯ anywhere (this includes ⋯⋯),
// i.e. whether the entry is a pattern rather than a literal argv.
func hasDynamicToken(args []string) bool {
	return slices.ContainsFunc(args, func(arg string) bool { return strings.Contains(arg, DynamicIdentifier) })
}

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
//  3. Entries are deduped by argv (envs unioned), and anything an existing
//     pattern already covers is absorbed *before* generalizing, so deltas a
//     stored profile already allows can never widen it.
//  4. The remaining argvs, except ⋯⋯ patterns, go into tries, one level per
//     argv position, with a separate trie per (argv[0], argc), so argvs of
//     different lengths never merge into each other. A node with more than
//     threshold distinct children — or with an existing ⋯ child — has its
//     children merged into a single ⋯ child, subtrees unioned.
//  5. The result is consolidated again (sound subsumption, see
//     consolidateExecs). If the binary still has more than threshold
//     distinct entries, it falls back to [argv0, ⋯⋯]: known binary, any
//     arguments.
//
// Envs of merged entries are unioned. Output is totally ordered (see
// compareExecCalls), and AnalyzeExecs(AnalyzeExecs(x)) == AnalyzeExecs(x).
// The result is never nil: stored profiles encode empty execs as [], as they
// did with DeflateStringer.
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
			if seenPassthrough.Add(execKey(e)) {
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

	slices.SortFunc(out, compareExecCalls)
	return out
}

// argRoot keys one argument trie: argvs only share a trie when they have the
// same argv[0] and the same length.
type argRoot struct {
	argv0 string
	argc  int
}

// analyzeBinaryExecs runs steps 2-5 of AnalyzeExecs for one binary.
func analyzeBinaryExecs(path string, execs []types.ExecCalls, threshold int) []types.ExecCalls {
	execs = collapseArgv0(execs, threshold)
	entries := consolidateExecs(dedupeByArgv(execs))

	roots := make(map[argRoot]*argNode)
	var anyArgs []types.ExecCalls
	for _, e := range entries {
		if slices.Contains(e.Args, ExecArgsWildcard) {
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

	// The ceiling counts what remains after consolidation: nothing an
	// existing pattern already covers may broaden the binary to [argv0, ⋯⋯].
	consolidated := consolidateExecs(append(anyArgs, patterns...))
	if len(consolidated) > threshold {
		return anyArgsPerArgv0(path, execs)
	}
	return consolidated
}

// dedupeByArgv merges entries with the same argv (injective key), unioning
// their envs. Order of first appearance is kept.
func dedupeByArgv(execs []types.ExecCalls) []types.ExecCalls {
	index := make(map[string]int, len(execs))
	envs := make([]mapset.Set[string], 0, len(execs))
	out := make([]types.ExecCalls, 0, len(execs))
	for _, e := range execs {
		key := tokensKey(e.Args)
		if i, ok := index[key]; ok {
			envs[i].Append(e.Envs...)
			continue
		}
		index[key] = len(out)
		envs = append(envs, mapset.NewThreadUnsafeSet(e.Envs...))
		out = append(out, types.ExecCalls{Path: e.Path, Args: e.Args})
	}
	for i := range out {
		out[i].Envs = sortedEnvs(envs[i])
	}
	return out
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

// consolidateExecs drops every entry another entry of the same binary
// covers, merging its envs into the coverer. Mirrors consolidateOpens, but
// patterns can be absorbed too, which is what keeps generalized deltas from
// widening a stored profile.
//
// Coverage is CompareExecArgs(coverer, covered) with the covered entry's
// tokens read as literal args. That is sound when the covered entry has no
// ⋯⋯: a covered ⋯ is only matched by a coverer's bare ⋯ or ⋯⋯, and a
// covered segment pattern only by an identical segment, a bare ⋯ or ⋯⋯.
// Entries containing ⋯⋯ are therefore never absorbed (only exact duplicates
// are merged, by dedupeByArgv), since a single ⋯ must not be taken to cover
// zero-or-more args.
//
// An entry is only absorbed into a coverer that is itself kept; entries whose
// only coverers are absorbed or mutually covering stay, which is always safe.
// Entries are processed in compareExecCalls order, so the result doesn't
// depend on input order. Input must be deduped by argv.
func consolidateExecs(execs []types.ExecCalls) []types.ExecCalls {
	sorted := slices.Clone(execs)
	slices.SortFunc(sorted, compareExecCalls)
	// Only patterns can cover anything else (distinct literals never match
	// each other), so candidate coverers are the patterns alone. This keeps a
	// first deflation of thousands of literal argvs linear in practice.
	var patterns []int
	anyArgs := make([]bool, len(sorted))
	for i := range sorted {
		anyArgs[i] = slices.Contains(sorted[i].Args, ExecArgsWildcard)
		if hasDynamicToken(sorted[i].Args) {
			patterns = append(patterns, i)
		}
	}
	if len(patterns) == 0 {
		return sorted
	}
	covers := func(j, i int) bool {
		// Without ⋯⋯ a coverer matches only argvs of its own length.
		if i == j || anyArgs[i] || (!anyArgs[j] && len(sorted[j].Args) != len(sorted[i].Args)) {
			return false
		}
		return CompareExecArgs(sorted[j].Args, sorted[i].Args)
	}

	covered := make([]bool, len(sorted))
	for i := range sorted {
		for _, j := range patterns {
			if covers(j, i) {
				covered[i] = true
				break
			}
		}
	}

	envs := make([]mapset.Set[string], len(sorted))
	var kept []int
	for i := range sorted {
		envs[i] = mapset.NewThreadUnsafeSet(sorted[i].Envs...)
		if !covered[i] {
			kept = append(kept, i)
		}
	}
	for i := range sorted {
		if !covered[i] {
			continue
		}
		into := -1
		for _, j := range kept {
			if hasDynamicToken(sorted[j].Args) && covers(j, i) {
				into = j
				break
			}
		}
		if into < 0 {
			kept = append(kept, i) // no kept coverer: keeping it is always safe
			continue
		}
		envs[into].Append(sorted[i].Envs...)
	}

	slices.Sort(kept)
	out := make([]types.ExecCalls, 0, len(kept))
	for _, i := range kept {
		e := sorted[i]
		e.Envs = sortedEnvs(envs[i])
		out = append(out, e)
	}
	return out
}

func sortedEnvs(set mapset.Set[string]) []string {
	if set.Cardinality() == 0 {
		return nil
	}
	return mapset.Sorted(set)
}
