package queuemanager

import (
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/config"
	"k8s.io/apiserver/pkg/endpoints/request"
)

type kindQueue struct {
	maxObjectSize int
	maxQueueLen   uint64
	queueLen      atomic.Uint64
	workerSem     chan struct{}
}

type QueueManager struct {
	queues sync.Map
	cfg    *config.Config
}

func NewQueueManager(cfg *config.Config) *QueueManager {
	return &QueueManager{
		cfg: cfg,
	}
}

func (qm *QueueManager) getOrCreateQueue(kind string) *kindQueue {
	// Attempt to load the queue directly
	if q, exists := qm.queues.Load(kind); exists {
		return q.(*kindQueue)
	}

	// Use a double-check pattern with sync.Map
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

	newQueue := &kindQueue{
		maxObjectSize: maxObjectSize,
		maxQueueLen:   uint64(queueLen),
		workerSem:     make(chan struct{}, workerCount),
	}

	// Store the new queue if it doesn't already exist
	actual, _ := qm.queues.LoadOrStore(kind, newQueue)
	if actual != newQueue {
		// Another goroutine created the queue, discard the new one
		return actual.(*kindQueue)
	}

	logger.L().Info("QueueManager - queue configuration", helpers.String("kind", kind), helpers.Int("queueLength", queueLen), helpers.Int("workerCount", workerCount), helpers.Int("maxObjectSize", maxObjectSize))
	return newQueue
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

func shouldSkipQueue(r *http.Request) bool {
	// FIXME find a way to limit the number of watches in parallel
	// watch requests cannot be queued, as they are long-lived and can block the queue
	if r.URL.Query().Get("watch") == "true" {
		return true
	}
	// check resourceVersion first
	resourceVersion := r.URL.Query().Get("resourceVersion")
	switch resourceVersion {
	case softwarecomposition.ResourceVersionMetadata:
		// Metadata requests do not require queuing
		return true
	case softwarecomposition.ResourceVersionFullSpec:
		// Full spec requests always require queuing
		return false
	}
	// skip if it's a watch, list, or follow request
	if r.URL.Query().Get("list") == "true" || r.URL.Query().Get("follow") == "true" {
		return true
	}
	// skip if it's a portforward or exec request
	if strings.HasSuffix(r.URL.Path, "/portforward") || strings.HasSuffix(r.URL.Path, "/exec") {
		return true
	}
	return false
}

func (qm *QueueManager) QueueHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind, verb := extractKindAndVerb(r)
		if kind == "unknown" ||
			shouldSkipQueue(r) {
			// These requests should not take up queue workers - they are not designed to be memory intensive (taking only metadata)
			// Unknown cannot be assigned to a queue
			next.ServeHTTP(w, r)
			return
		}
		q := qm.getOrCreateQueue(kind)
		// Enforce max object size if applicable (Content-Length header)
		if q.maxObjectSize > 0 && r.ContentLength > int64(q.maxObjectSize) {
			logger.L().Warning("QueueManager - request entity too large", helpers.String("path", r.URL.Path),
				helpers.String("kind", kind), helpers.String("verb", verb),
				helpers.Int("contentLength", int(r.ContentLength)), helpers.Int("maxObjectSize", q.maxObjectSize))
			http.Error(w, "Request entity too large", http.StatusRequestEntityTooLarge)
			return
		}
		// Check if the queue is full
		if q.queueLen.Add(1) > q.maxQueueLen {
			q.queueLen.Add(^uint64(0)) // Decrement back if the queue is full
			http.Error(w, "Too Many Requests (queue full)", http.StatusTooManyRequests)
			return
		}
		defer q.queueLen.Add(^uint64(0)) // Ensure decrement after processing
		// Limit concurrent workers (but also check for context cancellation)
		select {
		case <-r.Context().Done():
			logger.L().Debug("QueueManager - request context canceled", helpers.String("path", r.URL.Path),
				helpers.String("kind", kind), helpers.String("verb", verb),
				helpers.Int("queueLen", int(q.queueLen.Load())), helpers.Int("maxQueueLen", int(q.maxQueueLen)))
			http.Error(w, "Request Timeout", http.StatusRequestTimeout)
			return
		case q.workerSem <- struct{}{}:
			// check if the context is still valid
			if r.Context().Err() != nil {
				logger.L().Debug("QueueManager - request context canceled after semaphore", helpers.String("path", r.URL.Path),
					helpers.String("kind", kind), helpers.String("verb", verb),
					helpers.Int("queueLen", int(q.queueLen.Load())), helpers.Int("maxQueueLen", int(q.maxQueueLen)))
				<-q.workerSem // Release the semaphore
				http.Error(w, "Request Timeout", http.StatusRequestTimeout)
				return
			}
			defer func() { <-q.workerSem }() // Release semaphore when done
			next.ServeHTTP(w, r)
		}
	})
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
				logger.L().Warning("QueueManager - request took longer than timeout", helpers.String("path", r.URL.Path), helpers.String("method", r.Method), helpers.String("query", r.URL.RawQuery), helpers.String("remote", r.RemoteAddr))
			}
		}()
		next.ServeHTTP(w, r)
		close(done)
	})
}

// Example usage (wrap your main handler or router):
// http.Handle("/", TimeoutLoggerMiddleware(qm.QueueHandler(yourHandler)))
