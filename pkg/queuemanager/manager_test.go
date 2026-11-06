package queuemanager

import (
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

