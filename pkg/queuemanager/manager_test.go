package queuemanager

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apiserver/pkg/endpoints/request"
)

func TestExtractKindAndVerbFromPath(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		method   string
		wantKind string
		wantVerb string
	}{
		{
			name:     "With namespaces in path",
			url:      "/apis/spdx.softwarecomposition.kubescape.io/v1beta1/namespaces/foo/configurationscansummaries",
			method:   "GET",
			wantKind: "configurationscansummaries",
			wantVerb: "GET",
		},
		{
			name:     "With namespaces and different kind",
			url:      "/apis/spdx.softwarecomposition.kubescape.io/v1beta1/namespaces/default/applicationprofiles",
			method:   "POST",
			wantKind: "applicationprofiles",
			wantVerb: "POST",
		},
		{
			name:     "Without namespaces, fallback to 4th part",
			url:      "/apis/spdx.softwarecomposition.kubescape.io/v1beta1/sbomsyfts",
			method:   "PUT",
			wantKind: "sbomsyfts",
			wantVerb: "PUT",
		},
		{
			name:     "Short path, fallback to unknown",
			url:      "/foo/bar",
			method:   "PATCH",
			wantKind: "unknown",
			wantVerb: "PATCH",
		},
		{
			name:     "Path with only prefix",
			url:      "/apis/spdx.softwarecomposition.kubescape.io/v1beta1/",
			method:   "DELETE",
			wantKind: "unknown",
			wantVerb: "DELETE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.url, nil)
			kind, verb := extractKindAndVerbFromPath(req)
			assert.Equal(t, tt.wantKind, kind)
			assert.Equal(t, tt.wantVerb, verb)
		})
	}
}

func TestExtractKindAndVerb_Fallback(t *testing.T) {
	req := httptest.NewRequest("GET", "/foo/bar", nil)
	kind, verb := extractKindAndVerb(req)
	assert.Equal(t, "unknown", kind)
	assert.Equal(t, "GET", verb)
}

func TestExtractKindAndVerb_WithRequestInfo(t *testing.T) {
	tests := []struct {
		name     string
		info     *request.RequestInfo
		wantKind string
		wantVerb string
	}{
		{
			name: "Non-resource request returns unknown",
			info: &request.RequestInfo{
				IsResourceRequest: false,
				Path:              "/readyz",
				Verb:              "get",
			},
			wantKind: "unknown",
			wantVerb: "get",
		},
		{
			name: "Empty resource returns unknown",
			info: &request.RequestInfo{
				IsResourceRequest: true,
				Resource:          "",
				Verb:              "get",
			},
			wantKind: "unknown",
			wantVerb: "get",
		},
		{
			name: "Resource request returns resource and verb",
			info: &request.RequestInfo{
				IsResourceRequest: true,
				Resource:          "containerprofiles",
				Verb:              "list",
			},
			wantKind: "containerprofiles",
			wantVerb: "list",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/whatever", nil)
			req = req.WithContext(request.WithRequestInfo(req.Context(), tt.info))
			kind, verb := extractKindAndVerb(req)
			assert.Equal(t, tt.wantKind, kind)
			assert.Equal(t, tt.wantVerb, verb)
		})
	}
}

func TestQueueHandler_NonResourceBypass(t *testing.T) {
	cfg := &config.Config{
		DefaultWorkerCount: 1,
		DefaultQueueLength: 1,
	}
	qm := NewQueueManager(cfg)

	metricsEntered := make(chan struct{})
	releaseMetrics := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseMetrics) }) }
	defer release()
	handler := qm.QueueHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			close(metricsEntered)
			<-releaseMetrics
		}
		w.WriteHeader(http.StatusOK)
	}))

	// Start a non-resource request (/metrics) with RequestInfo attached
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req = req.WithContext(request.WithRequestInfo(req.Context(), &request.RequestInfo{
			IsResourceRequest: false,
			Path:              "/metrics",
			Verb:              "get",
		}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}()

	select {
	case <-metricsEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("metrics request did not enter handler")
	}

	// While /metrics is held in flight, other non-resource requests (/readyz, /livez)
	// must NOT be blocked or rejected by the queue.
	for _, path := range []string{"/readyz", "/livez"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(request.WithRequestInfo(req.Context(), &request.RequestInfo{
			IsResourceRequest: false,
			Path:              path,
			Verb:              "get",
		}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "%s must bypass queue and return 200", path)
	}

	release()
}

func queueCount(qm *QueueManager) int {
	count := 0
	qm.queues.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

func serveResource(handler http.Handler, resource string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/apis/spdx.softwarecomposition.kubescape.io/v1beta1/namespaces/default/"+resource, nil)
	req = req.WithContext(request.WithRequestInfo(req.Context(), &request.RequestInfo{
		IsResourceRequest: true,
		Resource:          resource,
		Verb:              "get",
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestQueueHandler_UnservedResourcesShareOneQueue(t *testing.T) {
	qm := NewQueueManager(&config.Config{DefaultQueueLength: 10, DefaultWorkerCount: 2})
	qm.SetServedResources([]string{"sbomsyfts"})
	handler := qm.QueueHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	for i := range 100 {
		rec := serveResource(handler, fmt.Sprintf("unserved%d", i))
		require.Equal(t, http.StatusNotFound, rec.Code)
	}
	assert.Zero(t, queueCount(qm), "a resource that is not served must not get a queue of its own")

	// the path fallback, for a request without RequestInfo, must not add queues either
	for i := range 100 {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/apis/spdx.softwarecomposition.kubescape.io/v1beta1/unserved%d", i), nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
	assert.Zero(t, queueCount(qm), "a path segment must not get a queue of its own")
}

func TestQueueHandler_ServedAndConfiguredResourcesGetOwnQueue(t *testing.T) {
	qm := NewQueueManager(&config.Config{
		DefaultQueueLength: 10,
		DefaultWorkerCount: 2,
		KindQueues:         map[string]config.KindQueueConfig{"sbomsyfts": {WorkerCount: 1}},
	})
	handler := qm.QueueHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// before the served resources are known, only a configured kind has its own queue
	serveResource(handler, "sbomsyfts")
	serveResource(handler, "containerprofiles")
	assert.Equal(t, 1, queueCount(qm))

	qm.SetServedResources([]string{"containerprofiles", "mcpservers", "mcpservers/mcp"})
	serveResource(handler, "containerprofiles")
	serveResource(handler, "mcpservers")
	assert.Equal(t, 3, queueCount(qm))
	_, ownQueue := qm.queues.Load("mcpservers/mcp")
	assert.False(t, ownQueue, "a subresource key must not add a queue")
}

func TestQueueHandler_SharedQueueIsBounded(t *testing.T) {
	qm := NewQueueManager(&config.Config{DefaultQueueLength: 1, DefaultWorkerCount: 1})
	entered := make(chan struct{})
	release := make(chan struct{})
	handler := qm.QueueHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveResource(handler, "unserved-a")
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not enter the handler")
	}

	// a different unserved resource lands in the same queue, which is full
	rec := serveResource(handler, "unserved-b")
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)

	close(release)
	<-done
}
