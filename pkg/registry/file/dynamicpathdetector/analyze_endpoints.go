package dynamicpathdetector

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

func isWildcardPort(port string) bool {
	return port == "0"
}

func rewritePort(endpoint, wildcardPort string) string {
	if wildcardPort == "" {
		return endpoint
	}
	port, pathPart := splitEndpointPortAndPath(endpoint)
	if !isWildcardPort(port) {
		return ":" + wildcardPort + pathPart
	}
	return endpoint
}

func AnalyzeEndpoints(endpoints *[]types.HTTPEndpoint, analyzer *PathAnalyzer) []types.HTTPEndpoint {
	if len(*endpoints) == 0 {
		return nil
	}

	// Detect wildcard port in input (port 0 means any port)
	wildcardPort := ""
	for _, ep := range *endpoints {
		port, _ := splitEndpointPortAndPath(ep.Endpoint)
		if isWildcardPort(port) {
			wildcardPort = port
			break
		}
	}

	// First pass: build tree, redirecting to wildcard port if needed
	for _, endpoint := range *endpoints {
		_, _ = AnalyzeURL(rewritePort(endpoint.Endpoint, wildcardPort), analyzer)
	}

	// Second pass: process endpoints
	var newEndpoints []*types.HTTPEndpoint
	for _, endpoint := range *endpoints {
		ep := endpoint
		ep.Endpoint = rewritePort(ep.Endpoint, wildcardPort)
		processedEndpoint, err := ProcessEndpoint(&ep, analyzer, newEndpoints)
		if processedEndpoint == nil && err == nil || err != nil {
			continue
		}
		newEndpoints = append(newEndpoints, processedEndpoint)
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

func splitEndpointPortAndPath(endpoint string) (string, string) {
	s := strings.TrimPrefix(endpoint, ":")
	idx := strings.Index(s, "/")
	if idx == -1 {
		return s, "/"
	}
	return s[:idx], s[idx:]
}

func MergeDuplicateEndpoints(endpoints []*types.HTTPEndpoint) []*types.HTTPEndpoint {
	seen := make(map[string]*types.HTTPEndpoint)
	var newEndpoints []*types.HTTPEndpoint
	for _, endpoint := range endpoints {
		key := getEndpointKey(endpoint)

		if existing, found := seen[key]; found {
			existing.Methods = MergeStrings(existing.Methods, endpoint.Methods)
			mergeHeaders(existing, endpoint)
			continue
		}

		// Check if a wildcard port variant already exists (port 0 means any port)
		port, pathPart := splitEndpointPortAndPath(endpoint.Endpoint)
		if !isWildcardPort(port) {
			wildcardKey := fmt.Sprintf(":%s%s|%s", "0", pathPart, endpoint.Direction)
			if existing, found := seen[wildcardKey]; found {
				existing.Methods = MergeStrings(existing.Methods, endpoint.Methods)
				mergeHeaders(existing, endpoint)
				continue
			}
		}

		seen[key] = endpoint
		newEndpoints = append(newEndpoints, endpoint)
	}

	return newEndpoints
}

func getEndpointKey(endpoint *types.HTTPEndpoint) string {
	return fmt.Sprintf("%s|%s", endpoint.Endpoint, endpoint.Direction)
}

func mergeHeaders(existing, new *types.HTTPEndpoint) {
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
