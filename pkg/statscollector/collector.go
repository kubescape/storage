package statscollector

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"k8s.io/apiserver/pkg/endpoints/request"
)

type Stats struct {
	Min   time.Duration
	Max   time.Duration
	Sum   time.Duration
	Count int64
}

type key struct {
	Kind string
	Verb string
}

type StatsCollector struct {
	mu    sync.RWMutex
	stats map[key]*Stats
}

func NewStatsCollector() *StatsCollector {
	return &StatsCollector{
		stats: make(map[key]*Stats),
	}
}

// GetStats returns a snapshot of the current statistics. If reset is true, statistics are cleared after reading.
func (sc *StatsCollector) GetStats(reset bool) map[string]map[string]Stats {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	result := make(map[string]map[string]Stats)
	for k, v := range sc.stats {
		if _, ok := result[k.Kind]; !ok {
			result[k.Kind] = make(map[string]Stats)
		}
		// Copy the struct to avoid race
		result[k.Kind][k.Verb] = *v
	}
	if reset {
		sc.stats = make(map[key]*Stats)
	}
	return result
}

// Handler returns a middleware that collects timing statistics for the downstream handler chain.
// It should be attached as close as possible to the actual API logic in the handler chain
// to measure only the core API logic execution time (excluding upstream middleware).
func (sc *StatsCollector) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind, verb := extractKindAndVerb(r)
		start := time.Now()
		next.ServeHTTP(w, r)
		elapsed := time.Since(start)

		k := key{Kind: kind, Verb: verb}
		sc.mu.Lock()
		s, ok := sc.stats[k]
		if !ok {
			s = &Stats{Min: elapsed, Max: elapsed, Sum: elapsed, Count: 1}
			sc.stats[k] = s
		} else {
			if elapsed < s.Min {
				s.Min = elapsed
			}
			if elapsed > s.Max {
				s.Max = elapsed
			}
			s.Sum += elapsed
			s.Count++
		}
		sc.mu.Unlock()
	})
}

func extractKindAndVerb(r *http.Request) (kind, verb string) {
	reqInfo, ok := request.RequestInfoFrom(r.Context())
	if ok {
		return reqInfo.Resource, reqInfo.Verb
	}
	// fallback:
	return extractKindAndVerbFromPath(r)
}

func extractKindAndVerbFromPath(r *http.Request) (kind, verb string) {
	// Example: /apis/spdx.softwarecomposition.kubescape.io/v1beta1/namespaces/foo/configurationscansummaries
	path := r.URL.Path
	if strings.HasPrefix(path, "/apis/spdx.softwarecomposition.kubescape.io/v1beta1/") {
		parts := strings.Split(path[1:], "/")
		// Look for "namespaces" in the path
		for i, part := range parts {
			if part == "namespaces" && i+2 < len(parts) {
				// The kind is after the namespace name
				return parts[i+2], r.Method
			}
		}
		// If "namespaces" is not present, fallback to the current logic
		if len(parts) >= 4 {
			return parts[3], r.Method
		}
	}
	return "unknown", r.Method
}
