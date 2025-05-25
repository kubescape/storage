package queuemanager

import (
	"net/http"
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
		// Start a dispatcher goroutine for this kind
		go func(q *kindQueue) {
			for req := range q.queue {
				req := req // capture loop variable
				_ = q.pool.Submit(func() {
					select {
					case <-req.r.Context().Done():
						// Client has gone away, do not process
						close(req.done)
						return
					default:
						// Still active, process
						req.next.ServeHTTP(req.w, req.r)
						close(req.done)
					}
				})
			}
		}(q)
		qm.queues[kind] = q
	}
	return q
}

func extractKindAndVerb(r *http.Request) (kind, verb string) {
	reqInfo, ok := request.RequestInfoFrom(r.Context())
	if !ok {
		return "unknown", r.Method
	}
	return reqInfo.Resource, reqInfo.Verb
}

func (qm *QueueManager) QueueHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind, verb := extractKindAndVerb(r)
		if verb == "watch" || kind == "unknown" {
			// Skip queue for watch or unknown kind
			// Watch requests should not take up queue workers - they are not designed to be memory intensive (taking only metadata)
			// Unknown cannot be assigned to a queue
			next.ServeHTTP(w, r)
			return
		}
		q := qm.getOrCreateQueue(kind)
		// Enforce max object size if applicable (Content-Length header)
		if q.maxObjectSize > 0 && r.ContentLength > int64(q.maxObjectSize) {
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
