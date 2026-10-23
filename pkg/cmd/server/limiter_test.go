package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/didip/tollbooth/v7"
	"github.com/kubescape/storage/pkg/apiserver"
	"github.com/stretchr/testify/require"
	apifilters "k8s.io/apiserver/pkg/endpoints/filters"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	genericapiserver "k8s.io/apiserver/pkg/server"
)

const limiterResourcePath = "/apis/spdx.softwarecomposition.kubescape.io/v1beta1/containerprofiles"

func TestConcurrentLimiterLongRunningClassification(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		want               int
	}{
		{"watch", "GET", limiterResourcePath + "?watch=true", 200},
		{"numeric watch", "GET", limiterResourcePath + "?watch=1", 200},
		{"namespaced watch", "GET", "/apis/spdx.softwarecomposition.kubescape.io/v1beta1/namespaces/default/containerprofiles?watch=true", 200},
		{"legacy watch", "GET", "/apis/spdx.softwarecomposition.kubescape.io/v1beta1/watch/containerprofiles", 200},
		{"watch list", "GET", limiterResourcePath + "?watch=true&sendInitialEvents=true&allowWatchBookmarks=true&resourceVersionMatch=NotOlderThan", 200},
		{"list", "GET", limiterResourcePath, 429},
		{"false watch", "GET", limiterResourcePath + "?watch=false", 429},
		{"get", "GET", limiterResourcePath + "/example?watch=true", 429},
		{"create", "POST", limiterResourcePath + "?watch=true", 429},
		{"update", "PUT", limiterResourcePath + "/example?watch=true", 429},
		{"patch", "PATCH", limiterResourcePath + "/example?watch=true", 429},
		{"delete", "DELETE", limiterResourcePath + "/example?watch=true", 429},
		{"discovery", "GET", "/apis/spdx.softwarecomposition.kubescape.io/v1beta1?watch=true", 429},
		{"health", "GET", "/readyz?watch=true", 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := genericapiserver.NewConfig(apiserver.Codecs)
			// No ordinary request can enter: only server-classified long-running requests may pass.
			lmt := NewConcurrentLimiter(0, cfg.LongRunningFunc)
			handler := apifilters.WithRequestInfo(lmt.LimitConcurrentRequests(tollbooth.NewLimiter(1000, nil),
				func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }), genericapiserver.NewRequestInfoResolver(cfg))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
			require.Equal(t, tc.want, response.Code)
		})
	}
}

func TestConcurrentLimiterOpenWatches(t *testing.T) {
	cfg := genericapiserver.NewConfig(apiserver.Codecs)
	lmt := NewConcurrentLimiter(10, cfg.LongRunningFunc)
	entered := make(chan struct{}, 10)
	release := make(chan struct{})
	handler := apifilters.WithRequestInfo(lmt.LimitConcurrentRequests(tollbooth.NewLimiter(1000, nil),
		func(w http.ResponseWriter, r *http.Request) {
			info, _ := apirequest.RequestInfoFrom(r.Context())
			if cfg.LongRunningFunc(r, info) {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				return
			}
			if r.Header.Get("X-Test-Block") == "true" {
				entered <- struct{}{}
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}
			w.WriteHeader(http.StatusOK)
		}), genericapiserver.NewRequestInfoResolver(cfg))
	srv := httptest.NewServer(handler)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := srv.Client()
	request := func(path string) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+path, nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		return resp
	}
	// Reproduce the reported 13 open watches against a 10-slot global limit.
	for i := 0; i < 13; i++ {
		resp := request(limiterResourcePath + "?watch=true")
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "watch %d", i+1)
	}
	for _, path := range []string{"/readyz", "/livez", "/apis/spdx.softwarecomposition.kubescape.io/v1beta1", limiterResourcePath} {
		resp := request(path)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, path)
	}
	// All ten slots remain available for ordinary work while the watches stay open.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+limiterResourcePath, nil)
			if err != nil {
				t.Error(err)
				return
			}
			req.Header.Set("X-Test-Block", "true")
			resp, err := client.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("ordinary request returned %d", resp.StatusCode)
			}
		}()
	}
	// Always unblock and join the goroutines, including when an assertion fails.
	defer func() { cancel(); wg.Wait() }()
	for i := 0; i < 10; i++ {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("ordinary requests did not acquire all ten slots")
		}
	}
	resp := request(limiterResourcePath)
	resp.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	// A new watch must also pass when ordinary requests have filled the global limit.
	resp = request(limiterResourcePath + "?watch=true")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	close(release)
	wg.Wait()
	resp = request(limiterResourcePath)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "slots must be released after ordinary requests finish")
}

func TestConcurrentLimiterPreservesPerClientLimit(t *testing.T) {
	for _, path := range []string{limiterResourcePath, limiterResourcePath + "?watch=true"} {
		t.Run(path, func(t *testing.T) {
			cfg := genericapiserver.NewConfig(apiserver.Codecs)
			ipLimiter := tollbooth.NewLimiter(0.001, nil).SetBurst(1)
			ipLimiter.SetIPLookups([]string{"X-Forwarded-For"})
			calls := 0
			handler := apifilters.WithRequestInfo(NewConcurrentLimiter(10, cfg.LongRunningFunc).LimitConcurrentRequests(ipLimiter,
				func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusOK) }), genericapiserver.NewRequestInfoResolver(cfg))
			for _, want := range []int{http.StatusOK, http.StatusTooManyRequests} {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Header.Set("X-Forwarded-For", "192.0.2.1")
				resp := httptest.NewRecorder()
				handler.ServeHTTP(resp, req)
				require.Equal(t, want, resp.Code)
			}
			require.Equal(t, 1, calls)
		})
	}
}

func TestConcurrentLimiterMissingClassification(t *testing.T) {
	cfg := genericapiserver.NewConfig(apiserver.Codecs)
	for _, check := range []apirequest.LongRunningRequestCheck{nil, cfg.LongRunningFunc} {
		handler := NewConcurrentLimiter(0, check).LimitConcurrentRequests(tollbooth.NewLimiter(1000, nil),
			func(w http.ResponseWriter, r *http.Request) { t.Error("request without classification bypassed limit") })
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, limiterResourcePath+"?watch=true", nil))
		require.Equal(t, http.StatusTooManyRequests, resp.Code)
	}
}

func TestConcurrentLimiterReleasesSlotOnPanic(t *testing.T) {
	lmt := NewConcurrentLimiter(1, nil)
	handler := lmt.LimitConcurrentRequests(tollbooth.NewLimiter(1000, nil),
		func(w http.ResponseWriter, r *http.Request) { panic("handler panic") })
	require.Panics(t, func() { handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil)) })
	handler = lmt.LimitConcurrentRequests(tollbooth.NewLimiter(1000, nil),
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, httptest.NewRequest("GET", "/", nil))
	require.Equal(t, http.StatusOK, resp.Code)
}
