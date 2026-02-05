package dynamicpathdetector

import (
	"errors"
	"maps"
	"slices"
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

func AnalyzeOpens(opens []types.OpenCalls, analyzer *PathAnalyzer, sbomSet mapset.Set[string]) ([]types.OpenCalls, error) {
	if opens == nil {
		return nil, nil
	}

	if sbomSet == nil {
		return nil, errors.New("sbomSet is nil")
	}

	dynamicOpens := make(map[string]types.OpenCalls)
	for _, open := range opens {
		_, _ = AnalyzeOpen(open.Path, analyzer)
	}

	for i := range opens {
		// sbomSet files have to be always present in the dynamicOpens
		if sbomSet.ContainsOne(opens[i].Path) {
			dynamicOpens[opens[i].Path] = opens[i]
			continue
		}

		result, err := AnalyzeOpen(opens[i].Path, analyzer)
		if err != nil {
			continue
		}

		if result != opens[i].Path {
			if existing, ok := dynamicOpens[result]; ok {
				existing.Flags = mapset.Sorted(mapset.NewThreadUnsafeSet(slices.Concat(existing.Flags, opens[i].Flags)...))
				dynamicOpens[result] = existing
			} else {
				dynamicOpen := types.OpenCalls{Path: result, Flags: opens[i].Flags}
				dynamicOpens[result] = dynamicOpen
			}
		} else {
			dynamicOpens[opens[i].Path] = opens[i]
		}
	}

	// Second pass to consolidate and apply multi-level collapses.
	// This handles cases where the first pass creates multiple dynamic paths
	// that should be further collapsed into a single path with an asterisk.
	finalOpens := make(map[string]types.OpenCalls)
	for _, open := range dynamicOpens {
		// Re-analyze the already partially-generalized path against the now fully-primed analyzer.
		finalPath, err := AnalyzeOpen(open.Path, analyzer)
		if err != nil {
			continue // Should not happen as paths are valid.
		}

		if existing, ok := finalOpens[finalPath]; ok {
			// If re-analysis caused a collapse (e.g., two '...' became '*'), merge the flags.
			existing.Flags = mapset.Sorted(mapset.NewThreadUnsafeSet(slices.Concat(existing.Flags, open.Flags)...))
			finalOpens[finalPath] = existing
		} else {
			// This is a new, fully generalized path.
			finalOpens[finalPath] = types.OpenCalls{Path: finalPath, Flags: open.Flags}
		}
	}

	return slices.SortedFunc(maps.Values(finalOpens), func(a, b types.OpenCalls) int {
		return strings.Compare(a.Path, b.Path)
	}), nil
}

func AnalyzeOpen(path string, analyzer *PathAnalyzer) (string, error) {
	return analyzer.AnalyzePath(path, "opens")
}
