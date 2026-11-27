package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/apiserver"
)

// fakeKubeAPI serves just enough of the core API for the NamespaceLifecycle
// admission plugin: a namespace list (one existing namespace), a hanging watch,
// and per-namespace GETs (existing → 200, anything else → 404).
func fakeKubeAPI(t *testing.T) *httptest.Server {
	t.Helper()
	existing := corev1.Namespace{
		TypeMeta:   metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "existing-ns", ResourceVersion: "1"},
		Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/namespaces" && r.URL.Query().Get("watch") == "true":
			// client-go's WatchList protocol: stream the initial state as ADDED
			// events, mark the end with a bookmark carrying the
			// k8s.io/initial-events-end annotation, then hold the watch open.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			enc := json.NewEncoder(w)
			if r.URL.Query().Get("sendInitialEvents") == "true" {
				_ = enc.Encode(map[string]any{"type": "ADDED", "object": existing})
				bookmark := existing
				bookmark.Annotations = map[string]string{"k8s.io/initial-events-end": "true"}
				_ = enc.Encode(map[string]any{"type": "BOOKMARK", "object": bookmark})
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-done // hold the watch open for the informer
		case r.URL.Path == "/api/v1/namespaces":
			list := corev1.NamespaceList{
				TypeMeta: metav1.TypeMeta{Kind: "NamespaceList", APIVersion: "v1"},
				ListMeta: metav1.ListMeta{ResourceVersion: "1"},
				Items:    []corev1.Namespace{existing},
			}
			writeJSON(w, http.StatusOK, list)
		case r.URL.Path == "/api/v1/namespaces/existing-ns":
			writeJSON(w, http.StatusOK, existing)
		case r.URL.Path == "/api" || r.URL.Path == "/apis" || r.URL.Path == "/api/v1":
			// minimal discovery so client-go does not choke
			writeJSON(w, http.StatusOK, map[string]any{})
		default:
			status := metav1.Status{
				Status:  metav1.StatusFailure,
				Code:    http.StatusNotFound,
				Reason:  metav1.StatusReasonNotFound,
				Message: "namespaces \"ghost\" not found",
				Details: &metav1.StatusDetails{Kind: "namespaces", Name: "ghost"},
			}
			writeJSON(w, http.StatusNotFound, status)
		}
	}))
	t.Cleanup(func() { close(done); srv.Close() })
	return srv
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeKubeconfig(t *testing.T, server string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	cfg := `apiVersion: v1
kind: Config
clusters:
- name: fake
  cluster: { server: "` + server + `" }
contexts:
- name: fake
  context: { cluster: fake, user: fake }
current-context: fake
users:
- name: fake
  user: {}
`
	require.NoError(t, os.WriteFile(path, []byte(cfg), 0o600))
	return path
}

// admissionTestConfig builds the server config against the fake kube API.
func admissionTestConfig(t *testing.T) *apiserver.Config {
	t.Helper()
	srv := fakeKubeAPI(t)
	o := newTestOptions()
	o.RecommendedOptions.Authentication = nil // nil-guarded; keeps the unit env off-cluster
	o.RecommendedOptions.SecureServing.ServerCert.CertDirectory = t.TempDir()
	o.RecommendedOptions.CoreAPI.CoreAPIKubeconfigPath = writeKubeconfig(t, srv.URL)
	cfg, err := o.Config()
	require.NoError(t, err)
	return cfg
}

// TestConfigWiresAdmissionControl pins the regression from upstream f9a16e2e
// ("upgrade sample api-server"): RecommendedOptions.Admission was nil'd, which
// silently disabled the ENTIRE admission chain — NamespaceLifecycle included —
// so writes into non-existent namespaces persist (issue #43).
func TestConfigWiresAdmissionControl(t *testing.T) {
	// Boot-time validation must pass too: cli.Run validates options before
	// serving, and it REQUIRES every registered plugin to appear in
	// RecommendedPluginOrder (a narrowed order boots nothing — observed live).
	vo := newTestOptions()
	require.Empty(t, vo.RecommendedOptions.Admission.Validate(),
		"admission options must survive boot-time validation")

	cfg := admissionTestConfig(t)
	require.NotNil(t, cfg.GenericConfig.AdmissionControl,
		"the admission chain must be wired — a nil chain means namespace lifecycle is not enforced (issue #43)")
	assert.True(t, cfg.GenericConfig.AdmissionControl.Handles(admission.Create))
}

// TestCreateIntoMissingNamespaceRejected pins the behavior: CREATE of an spdx
// object into a namespace that does not exist must be rejected (NotFound, as
// the kube-apiserver does for core resources), while an existing namespace
// admits the object.
func TestCreateIntoMissingNamespaceRejected(t *testing.T) {
	cfg := admissionTestConfig(t)
	require.NotNil(t, cfg.GenericConfig.AdmissionControl, "admission chain missing (issue #43)")

	stop := make(chan struct{})
	defer close(stop)
	// Wait on the NAMESPACE informer only: the factory also carries the API
	// priority-and-fairness informers, which the minimal fake API 404s.
	nsSynced := cfg.GenericConfig.SharedInformerFactory.Core().V1().Namespaces().Informer().HasSynced
	cfg.GenericConfig.SharedInformerFactory.Start(stop)
	deadline := time.Now().Add(20 * time.Second)
	for !nsSynced() {
		if time.Now().After(deadline) {
			t.Fatal("namespace informer never synced against the fake kube API")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// NamespaceLifecycle implements the MUTATING phase (Admit) — that is where
	// the real request path enforces namespace existence.
	mutator, ok := cfg.GenericConfig.AdmissionControl.(admission.MutationInterface)
	require.True(t, ok)
	oi := admission.NewObjectInterfacesFromScheme(apiserver.Scheme)
	gvk := schema.GroupVersionKind{Group: "spdx.softwarecomposition.kubescape.io", Version: "v1beta1", Kind: "ContainerProfile"}
	gvr := schema.GroupVersionResource{Group: "spdx.softwarecomposition.kubescape.io", Version: "v1beta1", Resource: "containerprofiles"}
	mkAttrs := func(ns string) admission.Attributes {
		obj := &v1beta1.ContainerProfile{ObjectMeta: metav1.ObjectMeta{Name: "cp", Namespace: ns}}
		return admission.NewAttributesRecord(obj, nil, gvk, ns, "cp", gvr, "", admission.Create, nil, false, &user.DefaultInfo{Name: "test"})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err := mutator.Admit(ctx, mkAttrs("ghost"), oi)
	require.Error(t, err, "create into a never-created namespace MUST be rejected")
	assert.True(t, apierrors.IsNotFound(err), "rejection is NotFound (the plugin returns the live-lookup error verbatim), got: %v", err)

	assert.NoError(t, mutator.Admit(ctx, mkAttrs("existing-ns"), oi),
		"create into an existing namespace must be admitted")
}
