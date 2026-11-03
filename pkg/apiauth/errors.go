package apiauth

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

const (
	reasonMissingHeader = "missing Authorization header"
	reasonNotBearer     = "expected Bearer token"
	reasonInvalidToken  = "invalid token"
	reasonBackend       = "authentication backend error"
)

type errorResponse struct {
	Error  string `json:"error"`
	Reason string `json:"reason"`
}

func writeError(w http.ResponseWriter, code int, errStr, reason string) {
	body, _ := json.Marshal(errorResponse{Error: errStr, Reason: reason})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

func writeUnauthenticated(w http.ResponseWriter, reason string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="kubescape"`)
	writeError(w, http.StatusUnauthorized, "unauthenticated", reason)
}

func writeForbidden(w http.ResponseWriter, reason string) {
	writeError(w, http.StatusForbidden, "forbidden", reason)
}

func writeUnavailable(w http.ResponseWriter) {
	writeError(w, http.StatusServiceUnavailable, "unavailable", reasonBackend)
}

// isBackendError reports whether an authenticator error came from failing to
// reach or use kube-apiserver, as opposed to kube-apiserver rejecting the
// token. kube-apiserver reports a rejected token as an unauthenticated
// TokenReview whose status.error is set, which the webhook authenticator turns
// into a plain error; the bearer token authenticator likewise turns "not
// authenticated" into a plain "invalid bearer token" error. Errors from the
// TokenReview call itself are API status errors (HTTP 4xx/5xx) or transport
// errors. Anything unrecognised is treated as a rejected token: the request is
// denied either way.
func isBackendError(err error) bool {
	if err == nil {
		return false
	}
	var agg utilerrors.Aggregate
	if errors.As(err, &agg) {
		for _, e := range agg.Errors() {
			if isBackendError(e) {
				return true
			}
		}
		return false
	}
	var status apierrors.APIStatus
	var netErr net.Error
	return errors.As(err, &status) || errors.As(err, &netErr) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}
