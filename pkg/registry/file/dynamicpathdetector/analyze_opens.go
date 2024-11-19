package dynamicpathdetector

import (
	"maps"
	"slices"
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

func AnalyzeOpens(opens []types.OpenCalls, analyzer *PathAnalyzer) ([]types.OpenCalls, error) {
	if opens == nil {
		return nil, nil
	}

	dynamicOpens := make(map[string]types.OpenCalls)
	for _, open := range opens {
		_, _ = AnalyzeOpen(open.Path, analyzer)
	}

	for i := range opens {
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

	return slices.SortedFunc(maps.Values(dynamicOpens), func(a, b types.OpenCalls) int {
		return strings.Compare(a.Path, b.Path)
	}), nil
}

func AnalyzeOpen(path string, analyzer *PathAnalyzer) (string, error) {
	return analyzer.AnalyzePath(path, "opens")
}
