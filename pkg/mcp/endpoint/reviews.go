package endpoint

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
)

// The access check of a read is answered from the authorizer's cache or by a
// SubjectAccessReview sent to kube-apiserver, possibly retried. k8s.io/apiserver
// does not say which, so the requests are counted where they leave storage: in
// the transport of the client the authorizer uses (CountReviews). A check that
// sent none was a cache hit, whatever the number of retries of one that did.

type reviewCountKey struct{}

// withReviewCount returns a context in which CountReviews counts the
// SubjectAccessReviews sent, and the counter.
func withReviewCount(ctx context.Context) (context.Context, *atomic.Int32) {
	n := new(atomic.Int32)
	return context.WithValue(ctx, reviewCountKey{}, n), n
}

// CountReviews wraps the transport of the authorizer's kube-apiserver client so
// that every SubjectAccessReview it sends is counted for the access check that
// sent it.
func CountReviews(rt http.RoundTripper) http.RoundTripper {
	return reviewCounter{rt}
}

type reviewCounter struct{ next http.RoundTripper }

func (c reviewCounter) RoundTrip(r *http.Request) (*http.Response, error) {
	if n, ok := r.Context().Value(reviewCountKey{}).(*atomic.Int32); ok &&
		strings.HasSuffix(r.URL.Path, "/subjectaccessreviews") {
		n.Add(1)
	}
	return c.next.RoundTrip(r)
}
