package statscollector

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
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

// A request that resolves to no kind must not open a stats bucket under the
// empty string: GetStats keys its result map on the kind, so an empty key is
// indistinguishable from a missing one for anything reading the snapshot.
func TestHandlerDoesNotRecordAnEmptyKind(t *testing.T) {
	sc := NewStatsCollector()
	h := sc.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest("DELETE", "/apis/spdx.softwarecomposition.kubescape.io/v1beta1/", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	snapshot := sc.GetStats(false)
	assert.NotContains(t, snapshot, "", "stats snapshot must not contain an empty kind")
	assert.Contains(t, snapshot, "unknown", "an unresolvable path belongs under the unknown kind")
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
