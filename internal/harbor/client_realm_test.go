package harbor

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRewriteAuthenticateRealmRewritesPrivateIPToTrustedHost(t *testing.T) {
	t.Parallel()

	realmBase, err := url.Parse("https://harbor-proxy:8443")
	require.NoError(t, err)

	header := `Bearer realm="https://192.168.10.196/service/token",service="harbor-registry"`
	rewritten, changed := rewriteAuthenticateRealm(header, realmBase)

	require.True(t, changed)
	assert.Equal(
		t,
		`Bearer realm="https://harbor-proxy:8443/service/token",service="harbor-registry"`,
		rewritten,
	)
}

func TestRewriteAuthenticateRealmLeavesNamedHostsUntouched(t *testing.T) {
	t.Parallel()

	realmBase, err := url.Parse("https://harbor-proxy:8443")
	require.NoError(t, err)

	header := `Bearer realm="https://registry.example.com/service/token",service="harbor-registry"`
	rewritten, changed := rewriteAuthenticateRealm(header, realmBase)

	require.False(t, changed)
	assert.Equal(t, header, rewritten)
}

func TestRewriteAuthenticateRealmTransportRewritesHeader(t *testing.T) {
	t.Parallel()

	realmBase, err := url.Parse("https://harbor-proxy:8443")
	require.NoError(t, err)

	transport := &rewriteAuthenticateRealmTransport{
		inner: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			resp := httptest.NewRecorder()
			resp.Header().Set("Www-Authenticate", `Bearer realm="https://192.168.10.196/service/token",service="harbor-registry"`)
			resp.WriteHeader(http.StatusUnauthorized)
			return resp.Result(), nil
		}),
		realmBase: realmBase,
	}

	req := httptest.NewRequest(http.MethodGet, "https://192.168.10.196:9443/v2/", nil)
	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(
		t,
		`Bearer realm="https://harbor-proxy:8443/service/token",service="harbor-registry"`,
		resp.Header.Get("Www-Authenticate"),
	)
}

func TestRegistryBaseURLPrefersAPIURLHost(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "https://harbor-proxy:8443", registryBaseURL("https://harbor-proxy:8443/api/v2.0", "https://192.168.10.196:9443"))
	assert.Equal(t, "https://harbor.example.com", registryBaseURL("", "https://harbor.example.com"))
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
