package apiauth

import (
	"net/http"
	"os"
	"strings"
)

const (
	// TokenFileEnv names the environment variable overriding the token file path.
	TokenFileEnv = "KS_API_TOKEN_FILE"
	// DefaultTokenFilePath is where the projected API token is mounted by default.
	DefaultTokenFilePath = "/var/run/secrets/kubescape.io/api-token/token"
)

// DefaultTokenFile returns $KS_API_TOKEN_FILE if set and non-empty, otherwise
// DefaultTokenFilePath.
func DefaultTokenFile() string {
	if p := os.Getenv(TokenFileEnv); p != "" {
		return p
	}
	return DefaultTokenFilePath
}

type bearerTransport struct {
	base  http.RoundTripper
	src   TokenSource
	hosts []string
}

// NewTransport returns a RoundTripper that adds "Authorization: Bearer <token>"
// to requests whose URL host matches one of hosts. Hosts are given as "host" or
// "host:port" exactly as they appear in the request URL, and are compared
// case-insensitively with no other normalisation.
//
// An existing Authorization header is never overwritten. If src.Token returns an
// error or an empty token, the request is sent without the header rather than
// failed; callers are expected to log token problems at a higher layer. The
// caller's request is never mutated. A nil base means http.DefaultTransport.
func NewTransport(base http.RoundTripper, src TokenSource, hosts ...string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &bearerTransport{base: base, src: src, hosts: hosts}
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("Authorization") != "" || !t.match(req.URL.Host) {
		return t.base.RoundTrip(req)
	}
	tok, err := t.src.Token()
	if err != nil || tok == "" {
		return t.base.RoundTrip(req)
	}
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+tok)
	return t.base.RoundTrip(req)
}

func (t *bearerTransport) match(host string) bool {
	for _, h := range t.hosts {
		if strings.EqualFold(host, h) {
			return true
		}
	}
	return false
}
