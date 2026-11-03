package apiauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
)

// The factories in k8s.io/apiserver post TokenReviews and SubjectAccessReviews
// through client.RESTClient(), which the client-go fake clientset returns as a
// nil *rest.RESTClient, so reactors are never reached. These tests therefore
// run a fake kube-apiserver over HTTP, as pkg/mcp/endpoint's tests do.

const testAudience = "kubescape.io"

type fakeAPIServer struct {
	mu   sync.Mutex
	trs  []authenticationv1.TokenReview
	sars []authorizationv1.SubjectAccessReview

	// tokenReview fills the status of a TokenReview, or returns an HTTP error code.
	tokenReview func(tr *authenticationv1.TokenReview) int
	// sar fills the status of a SubjectAccessReview, or returns an HTTP error code.
	sar func(sar *authorizationv1.SubjectAccessReview) int

	srv *httptest.Server
}

func authenticated(tr *authenticationv1.TokenReview) int {
	tr.Status = authenticationv1.TokenReviewStatus{
		Authenticated: true,
		User: authenticationv1.UserInfo{
			Username: "system:serviceaccount:kubescape:operator",
			UID:      "u1",
			Groups:   []string{"system:serviceaccounts", "system:serviceaccounts:kubescape"},
		},
		Audiences: []string{testAudience},
	}
	return 0
}

func allowed(sar *authorizationv1.SubjectAccessReview) int {
	sar.Status = authorizationv1.SubjectAccessReviewStatus{Allowed: true, Reason: "RBAC: allowed"}
	return 0
}

func newFakeAPIServer(t *testing.T) *fakeAPIServer {
	t.Helper()
	f := &fakeAPIServer{tokenReview: authenticated, sar: allowed}
	mux := http.NewServeMux()
	mux.HandleFunc("/apis/authentication.k8s.io/v1/tokenreviews", func(w http.ResponseWriter, r *http.Request) {
		var tr authenticationv1.TokenReview
		if err := json.NewDecoder(r.Body).Decode(&tr); err != nil {
			t.Errorf("decode TokenReview: %v", err)
		}
		f.mu.Lock()
		f.trs = append(f.trs, tr)
		f.mu.Unlock()
		tr.TypeMeta = metav1.TypeMeta{APIVersion: "authentication.k8s.io/v1", Kind: "TokenReview"}
		if code := f.tokenReview(&tr); code != 0 {
			writeStatus(w, code)
			return
		}
		writeJSON(w, &tr)
	})
	mux.HandleFunc("/apis/authorization.k8s.io/v1/subjectaccessreviews", func(w http.ResponseWriter, r *http.Request) {
		var sar authorizationv1.SubjectAccessReview
		if err := json.NewDecoder(r.Body).Decode(&sar); err != nil {
			t.Errorf("decode SubjectAccessReview: %v", err)
		}
		f.mu.Lock()
		f.sars = append(f.sars, sar)
		f.mu.Unlock()
		sar.TypeMeta = metav1.TypeMeta{APIVersion: "authorization.k8s.io/v1", Kind: "SubjectAccessReview"}
		if code := f.sar(&sar); code != 0 {
			writeStatus(w, code)
			return
		}
		writeJSON(w, &sar)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeStatus(w http.ResponseWriter, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(&metav1.Status{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"},
		Status:   metav1.StatusFailure,
		Code:     int32(code),
		Reason:   metav1.StatusReasonForbidden,
		Message:  "forbidden by fake apiserver",
	})
}

func (f *fakeAPIServer) tokenReviews() []authenticationv1.TokenReview {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]authenticationv1.TokenReview(nil), f.trs...)
}

func (f *fakeAPIServer) subjectAccessReviews() []authorizationv1.SubjectAccessReview {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]authorizationv1.SubjectAccessReview(nil), f.sars...)
}

func (f *fakeAPIServer) client(t *testing.T) kubernetes.Interface {
	t.Helper()
	c, err := kubernetes.NewForConfig(&restclient.Config{Host: f.srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var scansRoute = func(r *http.Request) (Attributes, bool) {
	if r.URL.Path == "/healthz" {
		return Attributes{}, false
	}
	return Attributes{Group: "kubescape.io", Resource: "scans", Verb: "create"}, true
}

// next records whether it ran and what user it saw.
type next struct {
	called bool
	user   string
	ok     bool
	authz  string
}

func (n *next) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n.called = true
	n.authz = r.Header.Get("Authorization")
	if u, ok := UserFrom(r.Context()); ok {
		n.user, n.ok = u.GetName(), true
	}
	w.WriteHeader(http.StatusNoContent)
}

func newMiddleware(t *testing.T, client kubernetes.Interface, route RouteFunc) func(http.Handler) http.Handler {
	t.Helper()
	mw, err := New(context.Background(), Config{Client: client, Audience: testAudience, Namespace: "kubescape", Route: route})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return mw
}

func serve(mw func(http.Handler) http.Handler, n *next, path, authz string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	rec := httptest.NewRecorder()
	mw(n).ServeHTTP(rec, req)
	return rec
}

type errorBody struct {
	Error  string `json:"error"`
	Reason string `json:"reason"`
}

func expectError(t *testing.T, rec *httptest.ResponseRecorder, n *next, code int, errStr, reason string) {
	t.Helper()
	if n.called {
		t.Fatalf("next handler ran on a %d", code)
	}
	if rec.Code != code {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var b errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	if b.Error != errStr || b.Reason != reason {
		t.Errorf("body = %+v, want error=%q reason=%q", b, errStr, reason)
	}
	wantWWW := ""
	if code == http.StatusUnauthorized {
		wantWWW = `Bearer realm="kubescape"`
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != wantWWW {
		t.Errorf("WWW-Authenticate = %q, want %q", got, wantWWW)
	}
}

func TestMiddleware_UnprotectedRoutePasses(t *testing.T) {
	f := newFakeAPIServer(t)
	n := &next{}
	rec := serve(newMiddleware(t, f.client(t), scansRoute), n, "/healthz", "")
	if !n.called || rec.Code != http.StatusNoContent {
		t.Fatalf("called=%v code=%d", n.called, rec.Code)
	}
	if len(f.tokenReviews())+len(f.subjectAccessReviews()) != 0 {
		t.Error("unprotected route triggered a review")
	}
}

func TestMiddleware_MissingHeader401(t *testing.T) {
	f := newFakeAPIServer(t)
	n := &next{}
	rec := serve(newMiddleware(t, f.client(t), scansRoute), n, "/v1/scan", "")
	expectError(t, rec, n, http.StatusUnauthorized, "unauthenticated", "missing Authorization header")
	if len(f.tokenReviews()) != 0 {
		t.Error("TokenReview sent without a token")
	}
}

func TestMiddleware_NotBearer401(t *testing.T) {
	for _, h := range []string{"Basic dXNlcjpwYXNz", "Bearer", "Bearer   ", "Bearer a b", "tok"} {
		t.Run(h, func(t *testing.T) {
			f := newFakeAPIServer(t)
			n := &next{}
			rec := serve(newMiddleware(t, f.client(t), scansRoute), n, "/v1/scan", h)
			expectError(t, rec, n, http.StatusUnauthorized, "unauthenticated", "expected Bearer token")
			if len(f.tokenReviews()) != 0 {
				t.Error("TokenReview sent for a malformed header")
			}
		})
	}
}

func TestMiddleware_SchemeCaseAndSpacing(t *testing.T) {
	for _, h := range []string{"bearer tok", "Bearer  tok", "BEARER tok "} {
		t.Run(h, func(t *testing.T) {
			f := newFakeAPIServer(t)
			n := &next{}
			rec := serve(newMiddleware(t, f.client(t), scansRoute), n, "/v1/scan", h)
			if !n.called || rec.Code != http.StatusNoContent {
				t.Fatalf("called=%v code=%d body=%q", n.called, rec.Code, rec.Body.String())
			}
			trs := f.tokenReviews()
			if len(trs) != 1 || trs[0].Spec.Token != "tok" {
				t.Fatalf("TokenReviews = %+v, want one for token \"tok\"", trs)
			}
		})
	}
}

func TestMiddleware_InvalidToken401(t *testing.T) {
	for name, status := range map[string]authenticationv1.TokenReviewStatus{
		"no error":   {Authenticated: false},
		"with error": {Authenticated: false, Error: "[invalid bearer token, token lookup failed]"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeAPIServer(t)
			f.tokenReview = func(tr *authenticationv1.TokenReview) int { tr.Status = status; return 0 }
			n := &next{}
			rec := serve(newMiddleware(t, f.client(t), scansRoute), n, "/v1/scan", "Bearer bad")
			expectError(t, rec, n, http.StatusUnauthorized, "unauthenticated", "invalid token")
			if len(f.subjectAccessReviews()) != 0 {
				t.Error("SubjectAccessReview sent for an unauthenticated request")
			}
		})
	}
}

func TestMiddleware_WrongAudience(t *testing.T) {
	f := newFakeAPIServer(t)
	f.tokenReview = func(tr *authenticationv1.TokenReview) int {
		authenticated(tr)
		tr.Status.Audiences = []string{"other"}
		return 0
	}
	n := &next{}
	rec := serve(newMiddleware(t, f.client(t), scansRoute), n, "/v1/scan", "Bearer tok")
	expectError(t, rec, n, http.StatusUnauthorized, "unauthenticated", "invalid token")
	trs := f.tokenReviews()
	if len(trs) != 1 || len(trs[0].Spec.Audiences) != 1 || trs[0].Spec.Audiences[0] != testAudience {
		t.Fatalf("TokenReviews = %+v, want one asking for audience %q", trs, testAudience)
	}
	if len(f.subjectAccessReviews()) != 0 {
		t.Error("SubjectAccessReview sent for a wrong-audience token")
	}
}

func TestMiddleware_Forbidden403(t *testing.T) {
	f := newFakeAPIServer(t)
	f.sar = func(sar *authorizationv1.SubjectAccessReview) int {
		sar.Status = authorizationv1.SubjectAccessReviewStatus{Allowed: false, Reason: "no RBAC policy matched"}
		return 0
	}
	n := &next{}
	rec := serve(newMiddleware(t, f.client(t), scansRoute), n, "/v1/scan", "Bearer tok")
	expectError(t, rec, n, http.StatusForbidden, "forbidden", "no RBAC policy matched")
}

func TestMiddleware_AllowedPassesAndSetsUser(t *testing.T) {
	f := newFakeAPIServer(t)
	n := &next{}
	rec := serve(newMiddleware(t, f.client(t), scansRoute), n, "/v1/scan", "Bearer tok")
	if !n.called || rec.Code != http.StatusNoContent {
		t.Fatalf("called=%v code=%d body=%q", n.called, rec.Code, rec.Body.String())
	}
	if !n.ok || n.user != "system:serviceaccount:kubescape:operator" {
		t.Fatalf("UserFrom = %q, %v", n.user, n.ok)
	}
}

func TestMiddleware_StripsBearerTokenBeforeNext(t *testing.T) {
	f := newFakeAPIServer(t)
	mw := newMiddleware(t, f.client(t), scansRoute)
	n := &next{}
	req := httptest.NewRequest(http.MethodPost, "/v1/scan", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	mw(n).ServeHTTP(rec, req)
	if !n.called || rec.Code != http.StatusNoContent {
		t.Fatalf("called=%v code=%d body=%q", n.called, rec.Code, rec.Body.String())
	}
	if n.authz != "" {
		t.Errorf("next saw Authorization %q, want none", n.authz)
	}
	if !n.ok || n.user != "system:serviceaccount:kubescape:operator" {
		t.Errorf("UserFrom = %q, %v", n.user, n.ok)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer tok" {
		t.Errorf("caller's request mutated: Authorization = %q", got)
	}
}

func TestMiddleware_ReviewError503(t *testing.T) {
	cases := map[string]func(f *fakeAPIServer){
		"tokenreview forbidden": func(f *fakeAPIServer) {
			f.tokenReview = func(*authenticationv1.TokenReview) int { return http.StatusForbidden }
		},
		"subjectaccessreview forbidden": func(f *fakeAPIServer) {
			f.sar = func(*authorizationv1.SubjectAccessReview) int { return http.StatusForbidden }
		},
		"apiserver down": func(f *fakeAPIServer) { f.srv.Close() },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakeAPIServer(t)
			mw := newMiddleware(t, f.client(t), scansRoute)
			setup(f)
			n := &next{}
			rec := serve(mw, n, "/v1/scan", "Bearer tok")
			expectError(t, rec, n, http.StatusServiceUnavailable, "unavailable", "authentication backend error")
		})
	}
}

func TestMiddleware_SARUsesRouteAttributes(t *testing.T) {
	cases := []struct {
		name   string
		attrs  Attributes
		wantNS string
	}{
		{"route namespace", Attributes{Group: "kubescape.io", Resource: "scans", Verb: "create", Namespace: "team-a"}, "team-a"},
		{"default namespace", Attributes{Group: "", Resource: "vulnerabilityscans", Verb: "get"}, "kubescape"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPIServer(t)
			route := func(*http.Request) (Attributes, bool) { return tc.attrs, true }
			n := &next{}
			rec := serve(newMiddleware(t, f.client(t), route), n, "/v1/scan", "Bearer tok")
			if !n.called || rec.Code != http.StatusNoContent {
				t.Fatalf("called=%v code=%d body=%q", n.called, rec.Code, rec.Body.String())
			}
			sars := f.subjectAccessReviews()
			if len(sars) != 1 {
				t.Fatalf("got %d SubjectAccessReviews, want 1", len(sars))
			}
			s := sars[0].Spec
			ra := s.ResourceAttributes
			if ra == nil || ra.Group != tc.attrs.Group || ra.Resource != tc.attrs.Resource || ra.Verb != tc.attrs.Verb || ra.Namespace != tc.wantNS {
				t.Fatalf("ResourceAttributes = %+v, want %+v in namespace %q", ra, tc.attrs, tc.wantNS)
			}
			if s.User != "system:serviceaccount:kubescape:operator" || s.UID != "u1" {
				t.Errorf("SAR user = %q uid = %q", s.User, s.UID)
			}
			if !contains(s.Groups, "system:serviceaccounts:kubescape") {
				t.Errorf("SAR groups = %v", s.Groups)
			}
		})
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func TestMiddleware_DisabledEnvPassesThrough(t *testing.T) {
	t.Setenv(DisabledEnv, "true")
	f := newFakeAPIServer(t)
	n := &next{}
	rec := serve(newMiddleware(t, f.client(t), scansRoute), n, "/v1/scan", "")
	if !n.called || rec.Code != http.StatusNoContent {
		t.Fatalf("called=%v code=%d", n.called, rec.Code)
	}
	if len(f.tokenReviews())+len(f.subjectAccessReviews()) != 0 {
		t.Error("disabled middleware sent a review")
	}
}

func TestMiddleware_DisabledEnvOnlyExactTrue(t *testing.T) {
	t.Setenv(DisabledEnv, "1")
	f := newFakeAPIServer(t)
	n := &next{}
	rec := serve(newMiddleware(t, f.client(t), scansRoute), n, "/v1/scan", "")
	expectError(t, rec, n, http.StatusUnauthorized, "unauthenticated", "missing Authorization header")
}

func TestMiddleware_CacheHitSkipsSecondReview(t *testing.T) {
	f := newFakeAPIServer(t)
	mw := newMiddleware(t, f.client(t), scansRoute)
	for i := range 2 {
		n := &next{}
		rec := serve(mw, n, "/v1/scan", "Bearer tok")
		if !n.called || rec.Code != http.StatusNoContent {
			t.Fatalf("request %d: called=%v code=%d", i, n.called, rec.Code)
		}
	}
	if got := len(f.tokenReviews()); got != 1 {
		t.Errorf("TokenReviews = %d, want 1", got)
	}
	if got := len(f.subjectAccessReviews()); got != 1 {
		t.Errorf("SubjectAccessReviews = %d, want 1", got)
	}
}

func TestNew_RequiresClientRouteAudience(t *testing.T) {
	f := newFakeAPIServer(t)
	c := f.client(t)
	for name, cfg := range map[string]Config{
		"no client":   {Audience: testAudience, Route: scansRoute},
		"no route":    {Client: c, Audience: testAudience},
		"no audience": {Client: c, Route: scansRoute},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "apiauth") {
				t.Fatalf("New err = %v, want an apiauth config error", err)
			}
		})
	}
}
