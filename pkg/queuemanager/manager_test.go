package queuemanager

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
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
