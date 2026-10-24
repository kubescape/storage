package server

import (
	"net/http"
	"sync"

	"github.com/didip/tollbooth/v7"
	"github.com/didip/tollbooth/v7/limiter"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
)

// inspired by https://stackoverflow.com/questions/73439068/limit-max-number-of-requests-per-hour-with-didip-tollbooth

type ConcurrentLimiter struct {
	max                     int
	current                 int
	mut                     sync.Mutex
	longRunningRequestCheck apirequest.LongRunningRequestCheck
}

func NewConcurrentLimiter(limit int, longRunningRequestCheck apirequest.LongRunningRequestCheck) *ConcurrentLimiter {
	return &ConcurrentLimiter{
		max:                     limit,
		longRunningRequestCheck: longRunningRequestCheck,
	}
}

func (limiter *ConcurrentLimiter) LimitConcurrentRequests(lmt *limiter.Limiter,
	handler func(http.ResponseWriter, *http.Request)) http.Handler {
	middle := func(w http.ResponseWriter, r *http.Request) {
		// Long-running requests still pass through Tollbooth, but must not hold
		// a concurrency slot for the lifetime of a watch.
		if requestInfo, ok := apirequest.RequestInfoFrom(r.Context()); ok &&
			limiter.longRunningRequestCheck != nil && limiter.longRunningRequestCheck(r, requestInfo) {
			handler(w, r)
			return
		}
		limiter.mut.Lock()
		maxHit := limiter.current == limiter.max
		if maxHit {
			limiter.mut.Unlock()
			http.Error(w, http.StatusText(429), http.StatusTooManyRequests)
			return
		}
		limiter.current += 1
		limiter.mut.Unlock()
		defer func() {
			limiter.mut.Lock()
			limiter.current -= 1
			limiter.mut.Unlock()
		}()
		// There's no rate-limit error, serve the next handler.
		handler(w, r)
	}
	return tollbooth.LimitHandler(lmt, http.HandlerFunc(middle))
}
