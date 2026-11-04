package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apiserver/pkg/authentication/authenticatorfactory"
	"k8s.io/apiserver/pkg/authentication/request/headerrequest"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/server/dynamiccertificates"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(t *testing.T, name string) *testCA {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return &testCA{cert: cert, key: key}
}

func (ca *testCA) pem() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw})
}

func (ca *testCA) clientCert(t *testing.T, cn string) *x509.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert
}

func newTestFrontProxyOnly(t *testing.T, frontProxyCA *testCA) *frontProxyOnly {
	caContent, err := dynamiccertificates.NewStaticCAContent("front-proxy-ca", frontProxyCA.pem())
	require.NoError(t, err)
	a, err := newFrontProxyOnly(&authenticatorfactory.RequestHeaderConfig{
		CAContentProvider:   caContent,
		AllowedClientNames:  headerrequest.StaticStringSlice{"front-proxy-client"},
		UsernameHeaders:     headerrequest.StaticStringSlice{"X-Remote-User"},
		UIDHeaders:          headerrequest.StaticStringSlice{"X-Remote-Uid"},
		GroupHeaders:        headerrequest.StaticStringSlice{"X-Remote-Group"},
		ExtraHeaderPrefixes: headerrequest.StaticStringSlice{"X-Remote-Extra-"},
	})
	require.NoError(t, err)
	return a
}

func TestNewFrontProxyOnlyRequiresRequestHeaderCA(t *testing.T) {
	_, err := newFrontProxyOnly(nil)
	assert.Error(t, err)
	_, err = newFrontProxyOnly(&authenticatorfactory.RequestHeaderConfig{})
	assert.Error(t, err)
}

func TestFrontProxyOnly(t *testing.T) {
	frontProxyCA := newTestCA(t, "front-proxy-ca")
	clusterCA := newTestCA(t, "cluster-ca")
	a := newTestFrontProxyOnly(t, frontProxyCA)

	const resourcePath = "/apis/spdx.softwarecomposition.kubescape.io/v1beta1/vulnerabilitymanifests"
	tests := []struct {
		name     string
		path     string
		cert     *x509.Certificate
		bearer   string
		wantOK   bool
		wantUser string
	}{
		{name: "front proxy is served as the forwarded user", path: resourcePath, cert: frontProxyCA.clientCert(t, "front-proxy-client"), wantOK: true, wantUser: "alice"},
		{name: "anonymous is refused", path: resourcePath},
		{name: "bearer token sent straight to storage is refused", path: resourcePath, bearer: "a-service-account-token"},
		{name: "cluster CA certificate is refused", path: resourcePath, cert: clusterCA.clientCert(t, "front-proxy-client")},
		{name: "front-proxy CA certificate with another name is refused", path: resourcePath, cert: frontProxyCA.clientCert(t, "someone-else")},
		{name: "readyz is served anonymously", path: "/readyz", wantOK: true, wantUser: user.Anonymous},
		{name: "livez subpath is served anonymously", path: "/livez/ping", wantOK: true, wantUser: user.Anonymous},
		{name: "healthz is served anonymously", path: "/healthz", wantOK: true, wantUser: user.Anonymous},
		{name: "a lookalike of a health path is refused", path: "/healthzz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "https://storage"+tt.path, nil)
			req.Header.Set("X-Remote-User", "alice")
			req.Header.Set("X-Remote-Group", "system:masters")
			if tt.cert != nil {
				req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{tt.cert}}
			}
			if tt.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tt.bearer)
			}
			resp, ok, _ := a.AuthenticateRequest(req)
			require.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantUser, resp.User.GetName())
			}
		})
	}
}

// frontProxyTestOptions authenticates against the fake kube API with no
// request-header CA, as on a cluster where the front-proxy CA is not published.
func frontProxyTestOptions(t *testing.T) *WardleServerOptions {
	t.Helper()
	kubeconfig := writeKubeconfig(t, fakeKubeAPI(t).URL)
	o := newTestOptions()
	o.RecommendedOptions.Authentication.RemoteKubeConfigFile = kubeconfig
	o.RecommendedOptions.Authentication.SkipInClusterLookup = true
	o.RecommendedOptions.SecureServing.ServerCert.CertDirectory = t.TempDir()
	o.RecommendedOptions.CoreAPI.CoreAPIKubeconfigPath = kubeconfig
	return o
}

func TestConfigRefusesToStartWithoutFrontProxyCA(t *testing.T) {
	_, err := frontProxyTestOptions(t).Config()
	assert.ErrorContains(t, err, "front-proxy")
}

func TestConfigInsecureSkipFrontProxyVerification(t *testing.T) {
	o := frontProxyTestOptions(t)
	o.StorageConfig.InsecureSkipFrontProxyVerification = true
	cfg, err := o.Config()
	require.NoError(t, err)
	_, isFrontProxyOnly := cfg.GenericConfig.Authentication.Authenticator.(*frontProxyOnly)
	assert.False(t, isFrontProxyOnly, "the delegating authenticator must be kept when verification is skipped")
}
