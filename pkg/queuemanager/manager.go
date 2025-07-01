package queuemanager

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/config"
	"github.com/panjf2000/ants/v2"
	"k8s.io/apiserver/pkg/endpoints/request"
)

type queuedRequest struct {
	w    http.ResponseWriter
	r    *http.Request
	next http.Handler
	done chan struct{}
}

type kindQueue struct {
	queue         chan *queuedRequest
	pool          *ants.Pool
	maxObjectSize int
}

type QueueManager struct {
	queues           map[string]*kindQueue
	lastQueueFullLog map[string]time.Time
	mu               sync.Mutex
	cfg              *config.Config
}

func NewQueueManager(cfg *config.Config) *QueueManager {
	return &QueueManager{
		queues:           make(map[string]*kindQueue),
		cfg:              cfg,
		lastQueueFullLog: make(map[string]time.Time),
	}
}

func (qm *QueueManager) getOrCreateQueue(kind string) *kindQueue {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	q, exists := qm.queues[kind]
	if !exists {
		// Get per-kind config or use defaults
		kcfg, ok := qm.cfg.KindQueues[kind]
		queueLen := qm.cfg.DefaultQueueLength
		workerCount := qm.cfg.DefaultWorkerCount
		maxObjectSize := qm.cfg.DefaultMaxObjectSize
		if ok {
			if kcfg.QueueLength > 0 {
				queueLen = kcfg.QueueLength
			}
			if kcfg.WorkerCount > 0 {
				workerCount = kcfg.WorkerCount
			}
			if kcfg.MaxObjectSize > 0 {
				maxObjectSize = kcfg.MaxObjectSize
			}
		}
		queue := make(chan *queuedRequest, queueLen)
		pool, _ := ants.NewPool(workerCount)
		q = &kindQueue{
			queue:         queue,
			pool:          pool,
			maxObjectSize: maxObjectSize,
		}
		logger.L().Info("queue configuration", helpers.String("kind", kind), helpers.Int("queueLength", queueLen), helpers.Int("workerCount", workerCount), helpers.Int("maxObjectSize", maxObjectSize))
		// Start a dispatcher goroutine for this kind
		go func(q *kindQueue) {
			for req := range q.queue {
				reqLocal := req // capture loop variable
				err := q.pool.Submit(func() {
					select {
					case <-reqLocal.r.Context().Done():
						// Client has gone away, do not process
						close(reqLocal.done)
						return
					default:
						// Still active, process
						reqLocal.next.ServeHTTP(reqLocal.w, reqLocal.r)
						close(reqLocal.done)
						return
					}
				})
				if err != nil {
					logger.L().Error("failed to submit to worker pool", helpers.Error(err), helpers.String("path", reqLocal.r.URL.Path), helpers.String("kind", kind), helpers.String("verb", reqLocal.r.Method), helpers.String("kind", kind))
					close(reqLocal.done)
				}
			}
		}(q)
		qm.queues[kind] = q
	}
	return q
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
	//          /apis/spdx.softwarecomposition.kubescape.io/v1beta1/namespaces/default/applicationprofiles
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
		if len(parts) >= 4 && parts[3] != "" {
			return parts[3], r.Method
		}
	}
	return "unknown", r.Method
}

func (qm *QueueManager) QueueHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind, verb := extractKindAndVerb(r)
		if kind == "unknown" || r.URL.Query().Get("watch") == "true" || r.URL.Query().Get("list") == "true" || r.URL.Query().Get("follow") == "true" ||
			strings.HasSuffix(r.URL.Path, "/portforward") || strings.HasSuffix(r.URL.Path, "/exec") {
			// Skip queue for watch, list, follow, portforward, exec, or unknown kind
			// These requests should not take up queue workers - they are not designed to be memory intensive (taking only metadata)
			// Unknown cannot be assigned to a queue
			next.ServeHTTP(w, r)
			return
		}
		q := qm.getOrCreateQueue(kind)
		// Enforce max object size if applicable (Content-Length header)
		if q.maxObjectSize > 0 && r.ContentLength > int64(q.maxObjectSize) {
			logger.L().Warning("request entity too large", helpers.String("path", r.URL.Path),
				helpers.String("kind", kind), helpers.String("verb", verb),
				helpers.Int("contentLength", int(r.ContentLength)), helpers.Int("maxObjectSize", q.maxObjectSize))
			http.Error(w, "Request entity too large", http.StatusRequestEntityTooLarge)
			return
		}
		req := &queuedRequest{
			w:    w,
			r:    r,
			next: next,
			done: make(chan struct{}),
		}
		select {
		case q.queue <- req:
			<-req.done
		default:
			qm.logQueueFullThrottled(kind, verb)
			http.Error(w, "Too Many Requests (queue full)", http.StatusTooManyRequests)
		}
	})
}

func (qm *QueueManager) logQueueFullThrottled(kind, verb string) {
	qm.mu.Lock()
	lastLog, ok := qm.lastQueueFullLog[kind]
	qm.mu.Unlock()
	if !ok || time.Since(lastLog) > 1*time.Minute {
		logger.L().Warning("queue full for resource", helpers.String("kind", kind), helpers.String("verb", verb), helpers.String("queue", "default"))
		qm.mu.Lock()
		qm.lastQueueFullLog[kind] = time.Now()
		qm.mu.Unlock()
	}
}

// TimeoutLoggerMiddleware logs a warning if a request takes longer than timeoutSeconds seconds to finish.
func TimeoutLoggerMiddleware(next http.Handler, timeoutSeconds int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") == "true" || r.URL.Query().Get("list") == "true" || r.URL.Query().Get("follow") == "true" ||
			strings.HasSuffix(r.URL.Path, "/portforward") || strings.HasSuffix(r.URL.Path, "/exec") {
			next.ServeHTTP(w, r)
			return
		}
		done := make(chan struct{})
		go func() {
			select {
			case <-done:
				// Request finished in time
			case <-time.After(time.Duration(timeoutSeconds) * time.Second):
				logger.L().Warning("Request took longer than timeout", helpers.String("path", r.URL.Path), helpers.String("method", r.Method), helpers.String("query", r.URL.RawQuery), helpers.String("remote", r.RemoteAddr))
			}
		}()
		next.ServeHTTP(w, r)
		close(done)
	})
}

// Example usage (wrap your main handler or router):
// http.Handle("/", TimeoutLoggerMiddleware(qm.QueueHandler(yourHandler)))
