package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kubescape/storage/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/user"
	genericapiserver "k8s.io/apiserver/pkg/server"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
)

// The queue manager answers 413 for a body over the size limit of the kind. A request that fails
// authentication must get 401 instead, which shows that the queue manager did not see it.
func TestQueueManagerRunsAfterAuthentication(t *testing.T) {
	o := newTestOptions()
	o.RecommendedOptions.Authentication = nil
	o.RecommendedOptions.SecureServing.ServerCert.CertDirectory = t.TempDir()
	o.RecommendedOptions.CoreAPI.CoreAPIKubeconfigPath = writeKubeconfig(t, fakeKubeAPI(t).URL)
	o.StorageConfig.QueueManagerEnabled = true
	o.StorageConfig.DefaultQueueLength = 10
	o.StorageConfig.DefaultWorkerCount = 1
	o.StorageConfig.KindQueues = map[string]config.KindQueueConfig{"sbomsyfts": {MaxObjectSize: 1}}
	cfg, err := o.Config()
	require.NoError(t, err)

	generic := &cfg.GenericConfig.Config
	generic.RequestInfoResolver = genericapiserver.NewRequestInfoResolver(generic)
	generic.FeatureGate = utilfeature.DefaultFeatureGate
	authenticated := false
	generic.Authentication.Authenticator = authenticator.RequestFunc(func(*http.Request) (*authenticator.Response, bool, error) {
		if !authenticated {
			return nil, false, nil
		}
		return &authenticator.Response{User: &user.DefaultInfo{Name: "test", Groups: []string{user.SystemPrivilegedGroup}}}, true, nil
	})
	chain := generic.BuildHandlerChainFunc(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), generic)

	post := func() int {
		req := httptest.NewRequest(http.MethodPost,
			"/apis/spdx.softwarecomposition.kubescape.io/v1beta1/namespaces/default/sbomsyfts", strings.NewReader("too large"))
		rec := httptest.NewRecorder()
		chain.ServeHTTP(rec, req)
		return rec.Code
	}

	assert.Equal(t, http.StatusUnauthorized, post())
	authenticated = true
	assert.Equal(t, http.StatusRequestEntityTooLarge, post())
}
