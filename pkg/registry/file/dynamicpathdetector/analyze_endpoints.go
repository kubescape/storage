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

func AnalyzeEndpoints(endpoints *[]types.HTTPEndpoint, analyzer *PathAnalyzer) []types.HTTPEndpoint {
	if len(*endpoints) == 0 {
		return nil
	}

	// First pass: build the analyzer trie from each endpoint's true (port,
	// path) tuple. Each port keys a separate sub-tree, so :0/foo and
	// :443/foo are analyzed independently — :443/foo is NOT rewritten to
	// :0/foo just because some unrelated endpoint also uses :0.
	for _, endpoint := range *endpoints {
		_, _ = AnalyzeURL(endpoint.Endpoint, analyzer)
	}

	// Second pass: process endpoints with their original ports.
	var newEndpoints []*types.HTTPEndpoint
	for _, endpoint := range *endpoints {
		ep := endpoint
		processedEndpoint, err := ProcessEndpoint(&ep, analyzer, newEndpoints)
		if processedEndpoint == nil && err == nil || err != nil {
			continue
		}
		newEndpoints = append(newEndpoints, processedEndpoint)
	}

	// Cross-port folding happens here: only same-(path, direction) siblings
	// of an explicit :0 wildcard get absorbed into it.
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

// MergeDuplicateEndpoints folds duplicates and merges same-path specific-port
// endpoints into a wildcard-port (:0) sibling. The folding is symmetric:
//
//   - If a specific-port endpoint is encountered AFTER its :0 sibling, the
//     specific-port methods/headers are merged INTO the wildcard entry.
//   - If a specific-port endpoint is encountered BEFORE its :0 sibling, it
//     is initially recorded; when the wildcard arrives we sweep `seen` for
//     same-(path, direction) specific-port siblings, fold them into the
//     wildcard, and remove them from the output.
//
// This contract was tightened on the back of upstream review on
// kubescape/storage#316 — a single :0 entry must NOT cause unrelated
// concrete-port endpoints to be wildcarded; only same-path siblings fold.
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

		port, pathPart := splitEndpointPortAndPath(endpoint.Endpoint)

		if isWildcardPort(port) {
			// Wildcard arriving after specific-port siblings — sweep `seen`
			// for any same-(path, direction) specific-port entries already
			// recorded, fold them into the wildcard, then drop them from
			// the output slice.
			absorbed := false
			for k, e := range seen {
				ePort, ePath := splitEndpointPortAndPath(e.Endpoint)
				if isWildcardPort(ePort) || ePath != pathPart || e.Direction != endpoint.Direction {
					continue
				}
				endpoint.Methods = MergeStrings(endpoint.Methods, e.Methods)
				mergeHeaders(endpoint, e)
				delete(seen, k)
				newEndpoints = removeEndpoint(newEndpoints, e)
				absorbed = true
			}
			seen[key] = endpoint
			newEndpoints = append(newEndpoints, endpoint)
			_ = absorbed
			continue
		}

		// Specific port: if a wildcard sibling for the same (path, direction)
		// is already in `seen`, fold this entry into it.
		wildcardKey := fmt.Sprintf(":0%s|%s", pathPart, endpoint.Direction)
		if existing, found := seen[wildcardKey]; found {
			existing.Methods = MergeStrings(existing.Methods, endpoint.Methods)
			mergeHeaders(existing, endpoint)
			continue
		}

		seen[key] = endpoint
		newEndpoints = append(newEndpoints, endpoint)
	}

	return newEndpoints
}

// removeEndpoint returns a new slice with the first occurrence of target
// removed (compared by pointer). Used by MergeDuplicateEndpoints when a
// previously-recorded specific-port entry is absorbed into a later wildcard.
func removeEndpoint(s []*types.HTTPEndpoint, target *types.HTTPEndpoint) []*types.HTTPEndpoint {
	for i, e := range s {
		if e == target {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
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
