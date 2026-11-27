package endpoint

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/authentication/authenticatorfactory"
	"k8s.io/apiserver/pkg/authentication/request/headerrequest"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/server/dynamiccertificates"
	basemetrics "k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/testutil"
)

const mcpPath = "/apis/spdx.softwarecomposition.kubescape.io/v1beta1/namespaces/kagent/mcpservers/default/mcp"

// testAdmission is an admission limit in front of two handlers that record
// which one served a request. The MCP handler blocks until it is released.
type testAdmission struct {
	handler http.Handler
	cert    *x509.Certificate
	release chan struct{}
	started chan string // the path of each request the MCP handler starts
	mu      sync.Mutex
	others  int
}

func newTestAdmission(t *testing.T, maxRunning, maxWaiting int, maxWait time.Duration) *testAdmission {
	t.Helper()
	frontCA := newTestCA(t, "front-proxy-ca")
	ca, err := dynamiccertificates.NewStaticCAContent("front-proxy-ca", frontCA.pem)
	require.NoError(t, err)
	ta := &testAdmission{cert: frontCA.clientCert(t, frontProxyName), release: make(chan struct{}), started: make(chan string, 20)}
	mcp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ta.started <- r.URL.Path
		<-ta.release
		w.WriteHeader(http.StatusOK)
	})
	// others stands in for storage's global limiter: an MCP call must never
	// reach it.
	others := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ta.mu.Lock()
		ta.others++
		ta.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	ta.handler, err = NewAdmission(AdmissionConfig{
		RequestHeader: &authenticatorfactory.RequestHeaderConfig{
			CAContentProvider:  ca,
			AllowedClientNames: headerrequest.StaticStringSlice{frontProxyName},
		},
		RequestInfoResolver: &genericapirequest.RequestInfoFactory{APIPrefixes: sets.NewString("api", "apis"), GrouplessAPIPrefixes: sets.NewString("api")},
		MaxRunning:          maxRunning,
		MaxWaiting:          maxWaiting,
		MaxWait:             maxWait,
	}, mcp, others)
	require.NoError(t, err)
	t.Cleanup(func() {
		select {
		case <-ta.release:
		default:
			close(ta.release)
		}
	})
	return ta
}

// serve sends a request on its own goroutine and returns its recorder, done
// once the handler has returned.
func (ta *testAdmission) serve(ctx context.Context, method, path string, cert *x509.Certificate) (*httptest.ResponseRecorder, <-chan struct{}) {
	req := httptest.NewRequest(method, path, nil).WithContext(ctx)
	if cert != nil {
		req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	}
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ta.handler.ServeHTTP(rec, req)
	}()
	return rec, done
}

func rejected(t *testing.T, reason string) float64 {
	t.Helper()
	v, err := testutil.GetCounterMetricValue(metrics.MCPAdmissionRejectedTotal.WithLabelValues(reason))
	require.NoError(t, err)
	return v
}

func gauge(t *testing.T, g basemetrics.GaugeMetric) float64 {
	t.Helper()
	v, err := testutil.GetGaugeMetricValue(g)
	require.NoError(t, err)
	return v
}

func waitFor(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestAdmissionPassesOtherRequestsOn(t *testing.T) {
	ta := newTestAdmission(t, 1, 0, time.Second)
	for _, path := range []string{
		"/apis/spdx.softwarecomposition.kubescape.io/v1beta1/namespaces/kubescape/vulnerabilitymanifests",
		"/apis/spdx.softwarecomposition.kubescape.io/v1beta1/namespaces/kubescape/mcpservers/default",
		"/metrics",
		"/healthz",
	} {
		rec, done := ta.serve(context.Background(), http.MethodGet, path, nil)
		waitFor(t, done, path)
		assert.Equal(t, http.StatusNoContent, rec.Code, path)
	}
	assert.Equal(t, 4, ta.others)
}

func TestAdmissionRefusesCallsNotFromKubeAPIServer(t *testing.T) {
	ta := newTestAdmission(t, 1, 0, time.Second)
	before := rejected(t, RejectNotFrontProxy)
	other := newTestCA(t, "cluster-client-ca").clientCert(t, frontProxyName)
	for _, cert := range []*x509.Certificate{nil, other} {
		rec, done := ta.serve(context.Background(), http.MethodPost, mcpPath, cert)
		waitFor(t, done, "refusal")
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	}
	assert.Equal(t, before+2, rejected(t, RejectNotFrontProxy))
	assert.Empty(t, ta.started, "no slot was taken")
	assert.Zero(t, ta.others)

	// A front-proxy call still gets the slot.
	_, done := ta.serve(context.Background(), http.MethodPost, mcpPath, ta.cert)
	assert.Equal(t, mcpPath, <-ta.started)
	close(ta.release)
	waitFor(t, done, "admitted call")
}

func TestAdmissionLimitsRunningAndWaitingCalls(t *testing.T) {
	ta := newTestAdmission(t, 1, 1, 5*time.Second)
	full := rejected(t, RejectQueueFull)

	// 1st call runs, 2nd waits, 3rd is refused at once with Retry-After.
	first, firstDone := ta.serve(context.Background(), http.MethodPost, mcpPath, ta.cert)
	<-ta.started
	assert.Equal(t, 1.0, gauge(t, metrics.MCPAdmissionRunning))
	second, secondDone := ta.serve(context.Background(), http.MethodPost, mcpPath, ta.cert)
	require.Eventually(t, func() bool { return gauge(t, metrics.MCPAdmissionWaiting) == 1 }, 5*time.Second, time.Millisecond)

	start := time.Now()
	third, thirdDone := ta.serve(context.Background(), http.MethodPost, mcpPath, ta.cert)
	waitFor(t, thirdDone, "third call")
	assert.Less(t, time.Since(start), time.Second, "refused without waiting")
	assert.Equal(t, http.StatusTooManyRequests, third.Code)
	assert.Equal(t, "1", third.Header().Get("Retry-After"))
	assert.Equal(t, full+1, rejected(t, RejectQueueFull))

	// The 1st call ends; the 2nd one runs.
	ta.release <- struct{}{}
	waitFor(t, firstDone, "first call")
	assert.Equal(t, http.StatusOK, first.Code)
	<-ta.started
	assert.Equal(t, 0.0, gauge(t, metrics.MCPAdmissionWaiting))
	ta.release <- struct{}{}
	waitFor(t, secondDone, "second call")
	assert.Equal(t, http.StatusOK, second.Code)
	assert.Equal(t, 0.0, gauge(t, metrics.MCPAdmissionRunning))
	assert.Zero(t, ta.others, "MCP calls never reach the global limiter")
}

func TestAdmissionWaitTimeout(t *testing.T) {
	ta := newTestAdmission(t, 1, 10, 50*time.Millisecond)
	before := rejected(t, RejectWaitTimeout)
	_, firstDone := ta.serve(context.Background(), http.MethodPost, mcpPath, ta.cert)
	<-ta.started

	start := time.Now()
	rec, done := ta.serve(context.Background(), http.MethodPost, mcpPath, ta.cert)
	waitFor(t, done, "waiting call")
	assert.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "1", rec.Header().Get("Retry-After"))
	assert.Equal(t, before+1, rejected(t, RejectWaitTimeout))
	assert.Equal(t, 0.0, gauge(t, metrics.MCPAdmissionWaiting))

	close(ta.release)
	waitFor(t, firstDone, "first call")
}

func TestAdmissionCallerGoneWhileWaiting(t *testing.T) {
	ta := newTestAdmission(t, 1, 10, 5*time.Second)
	before := rejected(t, RejectCallerGone)
	_, firstDone := ta.serve(context.Background(), http.MethodPost, mcpPath, ta.cert)
	<-ta.started

	ctx, cancel := context.WithCancel(context.Background())
	_, done := ta.serve(ctx, http.MethodPost, mcpPath, ta.cert)
	require.Eventually(t, func() bool { return gauge(t, metrics.MCPAdmissionWaiting) == 1 }, 5*time.Second, time.Millisecond)
	cancel()
	waitFor(t, done, "cancelled call")
	assert.Equal(t, before+1, rejected(t, RejectCallerGone))
	assert.Equal(t, 0.0, gauge(t, metrics.MCPAdmissionWaiting))

	// The slot it never took is still free for the next call.
	close(ta.release)
	waitFor(t, firstDone, "first call")
	_, nextDone := ta.serve(context.Background(), http.MethodPost, mcpPath, ta.cert)
	<-ta.started
	waitFor(t, nextDone, "next call")
}

func TestNewAdmissionNeedsItsConfig(t *testing.T) {
	ca, err := dynamiccertificates.NewStaticCAContent("front-proxy-ca", newTestCA(t, "front-proxy-ca").pem)
	require.NoError(t, err)
	ok := AdmissionConfig{
		RequestHeader:       &authenticatorfactory.RequestHeaderConfig{CAContentProvider: ca},
		RequestInfoResolver: &genericapirequest.RequestInfoFactory{APIPrefixes: sets.NewString("apis")},
		MaxRunning:          1, MaxWaiting: 0, MaxWait: time.Second,
	}
	_, err = NewAdmission(ok, http.NotFoundHandler(), http.NotFoundHandler())
	require.NoError(t, err)
	for name, change := range map[string]func(*AdmissionConfig){
		"no front proxy":   func(c *AdmissionConfig) { c.RequestHeader = nil },
		"no resolver":      func(c *AdmissionConfig) { c.RequestInfoResolver = nil },
		"no running calls": func(c *AdmissionConfig) { c.MaxRunning = 0 },
		"negative waiting": func(c *AdmissionConfig) { c.MaxWaiting = -1 },
		"no wait":          func(c *AdmissionConfig) { c.MaxWait = 0 },
	} {
		cfg := ok
		change(&cfg)
		_, err := NewAdmission(cfg, http.NotFoundHandler(), http.NotFoundHandler())
		assert.Error(t, err, name)
	}
}
