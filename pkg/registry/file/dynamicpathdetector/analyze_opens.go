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

	// Separate paths with asterisks to prevent them from being collapsed by the ellipsis logic.
	asteriskOpens := make(map[string]types.OpenCalls)
	normalOpens := []types.OpenCalls{}
	for _, open := range opens {
		if strings.Contains(open.Path, "*") {
			asteriskOpens[open.Path] = open
		} else {
			normalOpens = append(normalOpens, open)
		}
	}

	dynamicOpens := make(map[string]types.OpenCalls)
	for _, open := range normalOpens {
		_, _ = AnalyzeOpen(open.Path, analyzer)
	}

	for i := range normalOpens {
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

	// Add the asterisk paths back into the map for the next phase.
	for path, openCall := range asteriskOpens {
		dynamicOpens[path] = openCall
	}

	// TODO @constanze : check if this is really desireable -
	// Second pass: collapse paths that match an asterisk pattern.
	// This ensures that a more specific wildcard (*) "absorbs" less specific ones (...).
	finalOpens := make(map[string]types.OpenCalls)
	var asteriskPatterns []string

	// Separate asterisk patterns from other paths.
	for path, openCall := range dynamicOpens {
		if strings.Contains(path, "*") {
			asteriskPatterns = append(asteriskPatterns, path)
		}
		finalOpens[path] = openCall
	}

	// For each path, check if it matches any asterisk pattern.
	for path, openCall := range dynamicOpens {
		for _, pattern := range asteriskPatterns {
			// If a path matches an asterisk pattern, merge it and remove the original.
			if matched, _ := Match(pattern, path); matched && path != pattern {
				if existing, ok := finalOpens[pattern]; ok {
					existing.Flags = mapset.Sorted(mapset.NewThreadUnsafeSet(slices.Concat(existing.Flags, openCall.Flags)...))
					finalOpens[pattern] = existing
					delete(finalOpens, path)
				}
			}
		}
	}

	return slices.SortedFunc(maps.Values(finalOpens), func(a, b types.OpenCalls) int {
		return strings.Compare(a.Path, b.Path)
	}), nil
}

func AnalyzeOpen(path string, analyzer *PathAnalyzer) (string, error) {
	return analyzer.AnalyzePath(path, "opens")
}
