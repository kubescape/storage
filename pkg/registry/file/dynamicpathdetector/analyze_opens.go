package dynamicpathdetector

import (
	"fmt"

	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

func AnalyzeOpens(opens []types.OpenCalls, analyzer *PathAnalyzer) ([]types.OpenCalls, error) {
	var dynamicOpens []types.OpenCalls
	for _, open := range opens {
		AnalyzeOpen(open.Path, analyzer)
	}

	for i := range opens {
		result, err := AnalyzeOpen(opens[i].Path, analyzer)
		if err != nil {
			continue
		}

		if result != opens[i].Path {
			if existing, err := getIfExists(result, dynamicOpens); err == nil {
				existing.Flags = MergeStrings(existing.Flags, opens[i].Flags)
			} else {
				dynamicOpen := types.OpenCalls{Path: result, Flags: opens[i].Flags}
				dynamicOpens = append(dynamicOpens, dynamicOpen)
			}
		} else {
			dynamicOpens = append(dynamicOpens, opens[i])
		}
	}

	return dynamicOpens, nil
}

func AnalyzeOpen(path string, analyzer *PathAnalyzer) (string, error) {
	return analyzer.AnalyzePath(path, "opens")
}

func getIfExists(path string, dynamicOpens []types.OpenCalls) (*types.OpenCalls, error) {
	for i := range dynamicOpens {
		if dynamicOpens[i].Path == path {
			return &dynamicOpens[i], nil
		}
	}
	return nil, fmt.Errorf("not found")
}
