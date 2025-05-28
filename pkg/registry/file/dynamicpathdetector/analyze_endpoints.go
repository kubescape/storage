package dynamicpathdetector

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

func AnalyzeEndpoints(endpoints *[]types.HTTPEndpoint, analyzer *PathAnalyzer) []types.HTTPEndpoint {
	if len(*endpoints) == 0 {
		return nil
	}

	var newEndpoints []*types.HTTPEndpoint
	for _, endpoint := range *endpoints {
		_, _ = AnalyzeURL(endpoint.Endpoint, analyzer)
	}

	for _, endpoint := range *endpoints {
		processedEndpoint, err := ProcessEndpoint(&endpoint, analyzer, newEndpoints)
		if processedEndpoint == nil && err == nil || err != nil {
			continue
		} else {
			newEndpoints = append(newEndpoints, processedEndpoint)
		}
	}

	newEndpoints = MergeDuplicateEndpoints(newEndpoints)

	return convertPointerToValueSlice(newEndpoints)
}

func ProcessEndpoint(endpoint *types.HTTPEndpoint, analyzer *PathAnalyzer, newEndpoints []*types.HTTPEndpoint) (*types.HTTPEndpoint, error) {
	analyzeURL, err := AnalyzeURL(endpoint.Endpoint, analyzer)
	if err != nil {
		return nil, err
	}

	if analyzeURL != endpoint.Endpoint {
		endpoint.Endpoint = analyzeURL

		for i, e := range newEndpoints {
			if getEndpointKey(e) == getEndpointKey(endpoint) {
				newEndpoints[i].Methods = MergeStrings(e.Methods, endpoint.Methods)
				mergeHeaders(e, endpoint)
				return nil, nil
			}
		}

		dynamicEndpoint := types.HTTPEndpoint{
			Endpoint:  analyzeURL,
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

	if err := isValidURL(urlString); err != nil {
		return "", err
	}

	parsedURL, err := url.Parse(urlString)
	if err != nil {
		return "", err
	}

	port := parsedURL.Port()

	path, _ := analyzer.AnalyzePath(parsedURL.Path, port)
	if path == "/." {
		path = "/"
	}
	return ":" + port + path, nil
}

func MergeDuplicateEndpoints(endpoints []*types.HTTPEndpoint) []*types.HTTPEndpoint {
	seen := make(map[string]*types.HTTPEndpoint)
	var newEndpoints []*types.HTTPEndpoint
	for _, endpoint := range endpoints {
		key := getEndpointKey(endpoint)

		if existing, found := seen[key]; found {
			existing.Methods = MergeStrings(existing.Methods, endpoint.Methods)
			mergeHeaders(existing, endpoint)
		} else {
			seen[key] = endpoint
			newEndpoints = append(newEndpoints, endpoint)
		}
	}

	return newEndpoints
}

func getEndpointKey(endpoint *types.HTTPEndpoint) string {
	return fmt.Sprintf("%s|%s", endpoint.Endpoint, endpoint.Direction)
}

func mergeHeaders(existing, new *types.HTTPEndpoint) {
	// TODO: Find a better way to unmashal the headers
	existingHeaders, err := existing.GetHeaders()
	if err != nil {
		return
	}

	newHeaders, err := new.GetHeaders()
	if err != nil {
		return
	}

	for k, v := range newHeaders {
		if _, exists := existingHeaders[k]; exists {
			set := mapset.NewSet[string](append(existingHeaders[k], v...)...)
			existingHeaders[k] = set.ToSlice()
		} else {
			existingHeaders[k] = v
		}
	}

	rawJSON, err := json.Marshal(existingHeaders)
	if err != nil {
		fmt.Println("Error marshaling JSON:", err)
		return
	}

	existing.Headers = rawJSON
}

func convertPointerToValueSlice(m []*types.HTTPEndpoint) []types.HTTPEndpoint {
	result := make([]types.HTTPEndpoint, 0, len(m))
	for _, v := range m {
		if v != nil {
			result = append(result, *v)
		}
	}
	return result
}

func isValidURL(rawURL string) error {
	_, err := url.ParseRequestURI(rawURL)
	return err
}
