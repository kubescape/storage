package endpoint

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/metrics"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/authenticatorfactory"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
)

// Reasons an MCP call is refused by the admission limit, as in the
// storage_mcp_admission_rejected_total metric.
const (
	RejectNotFrontProxy = "not_front_proxy"
	RejectQueueFull     = "queue_full"
	RejectWaitTimeout   = "wait_timeout"
	RejectCallerGone    = "caller_gone"
)

// AdmissionConfig configures the admission limit of MCP calls.
type AdmissionConfig struct {
	// RequestHeader is kube-apiserver's front-proxy (request-header) config.
	RequestHeader *authenticatorfactory.RequestHeaderConfig
	// RequestInfoResolver tells MCP calls from every other request.
	RequestInfoResolver genericapirequest.RequestInfoResolver
	// MaxRunning is how many MCP calls run at once.
	MaxRunning int
	// MaxWaiting is how many more MCP calls wait for a free slot.
	MaxWaiting int
	// MaxWait is how long a call waits for a free slot before it gets 429.
	MaxWait time.Duration
}

// admission is the outermost handler of storage (designs-and-proposals
// storage-mcp-endpoint.md, section 6.6). MCP calls get their own slots and go
// straight to the handler chain, so they never hold a slot of storage's global
// limiter and cannot use up write capacity. Every other request goes on to
// the limiter unchanged.
type admission struct {
	cfg        AdmissionConfig
	frontProxy authenticator.Request
	running    chan struct{}
	mu         sync.Mutex
	waiting    int
	mcp        http.Handler
	others     http.Handler
}

// NewAdmission returns a handler that sends MCP calls to mcp, within the
// admission limit, and every other request to others. mcp is the handler
// chain without storage's global limiter; others is the same chain with it.
func NewAdmission(cfg AdmissionConfig, mcp, others http.Handler) (http.Handler, error) {
	if cfg.RequestHeader == nil || cfg.RequestHeader.CAContentProvider == nil {
		return nil, errors.New("the MCP admission limit needs the front-proxy (request-header) CA")
	}
	if cfg.RequestInfoResolver == nil {
		return nil, errors.New("the MCP admission limit needs a request info resolver")
	}
	if cfg.MaxRunning < 1 || cfg.MaxWaiting < 0 || cfg.MaxWait <= 0 {
		return nil, errors.New("the MCP admission limit needs at least 1 running call, no negative waiting calls and a positive wait")
	}
	return &admission{
		cfg:        cfg,
		frontProxy: frontProxyCheck(cfg.RequestHeader),
		running:    make(chan struct{}, cfg.MaxRunning),
		mcp:        mcp,
		others:     others,
	}, nil
}

func (a *admission) isMCP(r *http.Request) bool {
	info, err := a.cfg.RequestInfoResolver.NewRequestInfo(r)
	return err == nil && info.IsResourceRequest && info.APIGroup == softwarecomposition.GroupName &&
		info.Resource == "mcpservers" && info.Subresource == "mcp"
}

func (a *admission) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !a.isMCP(r) {
		a.others.ServeHTTP(w, r)
		return
	}
	// A caller that did not come through kube-apiserver gets 401 before it
	// takes a slot. The handler checks the user again (6.5).
	if _, viaAPIServer, _ := a.frontProxy.AuthenticateRequest(r); !viaAPIServer {
		a.reject(r, RejectNotFrontProxy)
		http.Error(w, "the MCP endpoint only accepts authenticated calls forwarded by kube-apiserver", http.StatusUnauthorized)
		return
	}
	start := time.Now()
	release, reason := a.acquire(r.Context())
	metrics.MCPAdmissionWait.Observe(time.Since(start).Seconds())
	if release == nil {
		a.reject(r, reason)
		if reason == RejectCallerGone {
			return
		}
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
		http.Error(w, "too many MCP calls in progress; retry later", http.StatusTooManyRequests)
		return
	}
	defer release()
	a.mcp.ServeHTTP(w, r)
}

// retryAfterSeconds is the Retry-After of a refused call: one call usually
// takes well under a second.
const retryAfterSeconds = 1

// acquire takes a running slot, waiting for one if MaxWaiting allows it. It
// returns the function that gives the slot back, or nil and why there is none.
func (a *admission) acquire(ctx context.Context) (func(), string) {
	select {
	case a.running <- struct{}{}:
		return a.release(), ""
	default:
	}

	a.mu.Lock()
	if a.waiting >= a.cfg.MaxWaiting {
		a.mu.Unlock()
		return nil, RejectQueueFull
	}
	a.waiting++
	metrics.MCPAdmissionWaiting.Set(float64(a.waiting))
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.waiting--
		metrics.MCPAdmissionWaiting.Set(float64(a.waiting))
		a.mu.Unlock()
	}()

	timer := time.NewTimer(a.cfg.MaxWait)
	defer timer.Stop()
	select {
	case a.running <- struct{}{}:
		return a.release(), ""
	case <-timer.C:
		return nil, RejectWaitTimeout
	case <-ctx.Done():
		return nil, RejectCallerGone
	}
}

func (a *admission) release() func() {
	metrics.MCPAdmissionRunning.Inc()
	return func() {
		metrics.MCPAdmissionRunning.Dec()
		<-a.running
	}
}

// reject counts a refused call. Only a call that did not come through
// kube-apiserver is logged at Info: calls refused for capacity can come in
// thousands under load, and the metric already counts them.
func (a *admission) reject(r *http.Request, reason string) {
	metrics.MCPAdmissionRejectedTotal.WithLabelValues(reason).Inc()
	details := []helpers.IDetails{helpers.String("reason", reason), helpers.String("remote", r.RemoteAddr)}
	if reason == RejectNotFrontProxy {
		logger.L().Info("mcp: call refused by the admission limit", details...)
		return
	}
	logger.L().Debug("mcp: call refused by the admission limit", details...)
}
