package dynamicpathdetector

import (
	"net/url"
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

func AnalyzeEndpoints(endpoints *[]types.HTTPEndpoint, analyzer *PathAnalyzer) ([]types.HTTPEndpoint, error) {
	var newEndpoints []types.HTTPEndpoint

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
