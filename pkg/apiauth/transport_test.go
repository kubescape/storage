package apiauth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

type stubSource struct {
	tok string
	err error
}

func (s stubSource) Token() (string, error) { return s.tok, s.err }

// do sends a GET to the test server through a transport configured with hosts
// and returns the Authorization header the server observed.
func do(t *testing.T, src TokenSource, preset string, hostsFor func(host string) []string) string {
	t.Helper()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Transport: NewTransport(nil, src, hostsFor(u.Host)...)}
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	if preset != "" {
		req.Header.Set("Authorization", preset)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if preset != "" && req.Header.Get("Authorization") != preset {
		t.Fatal("caller request was mutated")
	}
	return got
}

func self(h string) []string { return []string{h} }

func TestTransport_AddsHeaderForListedHost(t *testing.T) {
	if got := do(t, stubSource{tok: "abc"}, "", self); got != "Bearer abc" {
		t.Fatalf("got %q", got)
	}
}

func TestTransport_NoHeaderForOtherHost(t *testing.T) {
	got := do(t, stubSource{tok: "abc"}, "", func(string) []string { return []string{"other.example:1"} })
	if got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestTransport_NoHeaderWhenTokenEmpty(t *testing.T) {
	if got := do(t, stubSource{}, "", self); got != "" {
		t.Fatalf("empty token: got %q", got)
	}
	if got := do(t, stubSource{tok: "abc", err: http.ErrNotSupported}, "", self); got != "" {
		t.Fatalf("token error: got %q", got)
	}
}

func TestTransport_KeepsExistingAuthorization(t *testing.T) {
	if got := do(t, stubSource{tok: "abc"}, "Basic xyz", self); got != "Basic xyz" {
		t.Fatalf("got %q", got)
	}
}

func TestDefaultTokenFile_EnvOverride(t *testing.T) {
	t.Setenv(TokenFileEnv, "")
	if got := DefaultTokenFile(); got != DefaultTokenFilePath {
		t.Fatalf("got %q", got)
	}
	t.Setenv(TokenFileEnv, "/tmp/x")
	if got := DefaultTokenFile(); got != "/tmp/x" {
		t.Fatalf("got %q", got)
	}
}
