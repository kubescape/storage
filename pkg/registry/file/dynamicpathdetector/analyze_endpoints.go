package dynamicpathdetector

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

func AnalyzeEndpoints(endpoints *[]types.HTTPEndpoint, analyzer *PathAnalyzer) ([]types.HTTPEndpoint, error) {
	if len(*endpoints) == 0 {
		return nil, nil
	}

	var newEndpoints []*types.HTTPEndpoint
	for _, endpoint := range *endpoints {
		AnalyzeURL(endpoint.Endpoint, analyzer)
	}

	for _, endpoint := range *endpoints {
		processedEndpoint, err := ProcessEndpoint(&endpoint, analyzer, newEndpoints)
		if processedEndpoint == nil && err == nil || err != nil {
			continue
		} else {
			newEndpoints = append(newEndpoints, processedEndpoint)
		}
	}

	newEndpoints, err := MergeDuplicateEndpoints(newEndpoints)
	if err != nil {
		return nil, err
	}

	return convertPointerToValueSlice(newEndpoints), nil
}

func ProcessEndpoint(endpoint *types.HTTPEndpoint, analyzer *PathAnalyzer, newEndpoints []*types.HTTPEndpoint) (*types.HTTPEndpoint, error) {
	url, err := AnalyzeURL(endpoint.Endpoint, analyzer)
	if err != nil {
		return nil, err
	}

	if url != endpoint.Endpoint {

		// Check if this dynamic exists
		for i, e := range newEndpoints {
			if e.Endpoint == url {
				newEndpoints[i].Methods = mergeMethods(e.Methods, endpoint.Methods)
				mergeHeaders(e, endpoint)
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

	if err := isValidURL(urlString); err != nil {
		return "", err
	}

	parsedURL, err := url.Parse(urlString)
	if err != nil {
		return "", err
	}

	hostname := parsedURL.Hostname()

	path, _ := analyzer.AnalyzePath(parsedURL.Path, hostname)
	if path == "/." {
		path = "/"
	}
	return hostname + path, nil
}

func MergeDuplicateEndpoints(endpoints []*types.HTTPEndpoint) ([]*types.HTTPEndpoint, error) {
	seen := make(map[string]*types.HTTPEndpoint)
	var newEndpoints []*types.HTTPEndpoint
	for _, endpoint := range endpoints {
		key := getEndpointKey(endpoint)

		if existing, found := seen[key]; found {
			existing.Methods = mergeMethods(existing.Methods, endpoint.Methods)
			mergeHeaders(existing, endpoint)
		} else {
			seen[key] = endpoint
			newEndpoints = append(newEndpoints, endpoint)
		}
	}
	return newEndpoints, nil
}

func getEndpointKey(endpoint *types.HTTPEndpoint) string {
	return fmt.Sprintf("%s|%v|%v", endpoint.Endpoint, endpoint.Internal, endpoint.Direction)
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
