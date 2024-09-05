package dynamicpathdetector

import (
	"fmt"
	"net/url"
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

func AnalyzeEndpoints(endpoints *[]types.HTTPEndpoint, analyzer *PathAnalyzer) ([]types.HTTPEndpoint, error) {
	var newEndpoints []types.HTTPEndpoint
	MergeDuplicateEndpoints(endpoints)
	for _, endpoint := range *endpoints {
		AnalyzeURL(endpoint.Endpoint, analyzer)
	}

	for _, endpoint := range *endpoints {
		processedEndpoint, err := ProcessEndpoint(&endpoint, analyzer, newEndpoints)
		if processedEndpoint == nil && err == nil || err != nil {
			continue
		} else {
			newEndpoints = append(newEndpoints, *processedEndpoint)
		}
	}

	return newEndpoints, nil
}

func ProcessEndpoint(endpoint *types.HTTPEndpoint, analyzer *PathAnalyzer, newEndpoints []types.HTTPEndpoint) (*types.HTTPEndpoint, error) {
	url, err := AnalyzeURL(endpoint.Endpoint, analyzer)
	if err != nil {
		return nil, err
	}

	if url != endpoint.Endpoint {

		// Check if this dynamic exists
		for i, e := range newEndpoints {
			if e.Endpoint == url {
				newEndpoints[i].Methods = mergeMethods(e.Methods, endpoint.Methods)
				newEndpoints[i].Headers = mergeHeaders(e.Headers, endpoint.Headers)
				return nil, nil
			}
		}

		dynamicEndpoint := types.HTTPEndpoint{
			Endpoint:  url,
			Methods:   endpoint.Methods,
			Internal:  endpoint.Internal,
			Direction: endpoint.Direction,
			Headers:   endpoint.Headers,
		}

		return &dynamicEndpoint, nil
	}

	return endpoint, nil
}

func AnalyzeURL(urlString string, analyzer *PathAnalyzer) (string, error) {
	if !strings.HasPrefix(urlString, "http://") && !strings.HasPrefix(urlString, "https://") {
		urlString = "http://" + urlString
	}

	parsedURL, err := url.Parse(urlString)
	if err != nil {
		return "", err
	}

	hostname := parsedURL.Hostname()

	path, _ := analyzer.AnalyzePath(parsedURL.Path, hostname)
	return hostname + path, nil
}

func MergeDuplicateEndpoints(endpoints *[]types.HTTPEndpoint) {
	seen := make(map[string]*types.HTTPEndpoint)
	newEndpoints := make([]types.HTTPEndpoint, 0)

	for i := range *endpoints {
		endpoint := &(*endpoints)[i]
		key := getEndpointKey(endpoint)

		if existing, found := seen[key]; found {
			existing.Methods = mergeMethods(existing.Methods, endpoint.Methods)
			existing.Headers = mergeHeaders(existing.Headers, endpoint.Headers)
		} else {
			seen[key] = endpoint
			newEndpoints = append(newEndpoints, *endpoint)
		}
	}

	*endpoints = newEndpoints
}

func getEndpointKey(endpoint *types.HTTPEndpoint) string {
	return fmt.Sprintf("%s|%v|%v", endpoint.Endpoint, endpoint.Internal, endpoint.Direction)
}

func mergeHeaders(existing, new map[string][]string) map[string][]string {

	for k, v := range new {
		if _, exists := existing[k]; exists {
			set := mapset.NewSet[string](append(existing[k], v...)...)
			existing[k] = set.ToSlice()
		} else {
			existing[k] = v
		}
	}

	return existing
}

func mergeMethods(existing, new []string) []string {
	methodSet := make(map[string]bool)
	for _, m := range existing {
		methodSet[m] = true
	}
	for _, m := range new {
		if !methodSet[m] {
			existing = append(existing, m)
			methodSet[m] = true
		}
	}
	return existing
}
