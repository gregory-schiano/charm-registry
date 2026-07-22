package oci

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gschiano/charm-registry/internal/config"
	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/repo"
)

func TestSyncPackageGeneratesCredentialsAndImageReference(t *testing.T) {
	client := testClient(repo.NewMemory())
	pkg := core.Package{
		ID:   "pkg-1",
		Name: "Spring_Petclinic",
	}
	synced, err := client.SyncPackage(context.Background(), pkg)
	require.NoError(t, err)
	assert.Equal(t, "charm-spring-petclinic", synced.OCIProject)
	require.NotNil(t, synced.OCIPushRobot)
	require.NotNil(t, synced.OCIPullRobot)
	assert.Equal(t, "push-pkg-1", synced.OCIPushRobot.Username)
	assert.Equal(t, "pull-pkg-1", synced.OCIPullRobot.Username)
	assert.NotEmpty(t, synced.OCIPushRobot.EncryptedSecret)
	assert.NotEmpty(t, synced.OCIPullRobot.EncryptedSecret)
	assert.NotNil(t, synced.OCISyncedAt)

	image, err := client.ImageReference(synced, "app_image")
	require.NoError(t, err)
	assert.Equal(t, "registry.example.com/charm-spring-petclinic/app-image", image)

}

func TestCredentialsDecryptGeneratedCredential(t *testing.T) {
	client := testClient(repo.NewMemory())
	pkg, err := client.SyncPackage(context.Background(), core.Package{ID: "pkg-1", Name: "demo"})
	require.NoError(t, err)
	username, password, err := client.Credentials(pkg, true)
	require.NoError(t, err)
	assert.Equal(t, "pull-pkg-1", username)
	assert.NotEmpty(t, password)

}

func TestDeriveKeyUsesPBKDF2(t *testing.T) {
	t.Parallel()

	key, err := deriveKey("test-secret")
	require.NoError(t, err)
	rawSHA := sha256.Sum256([]byte("test-secret"))

	assert.Len(t, key, 32)
	assert.NotEqual(t, rawSHA[:], key)
}

func TestCheckManifestDescriptorRejectsOversizedManifest(t *testing.T) {
	t.Parallel()

	client := testClient(repo.NewMemory())
	client.maxManifestBytes = 4

	err := client.checkManifestDescriptor(v1.Descriptor{Size: 5}, 5)

	require.ErrorContains(t, err, "source OCI manifest is 5 bytes, exceeds 4 bytes")
}

func TestStorageParametersSelectsFilesystemDriver(t *testing.T) {
	t.Parallel()

	storage := storageParameters(config.Config{
		OCIStorageBackend: config.StorageBackendFilesystem,
		OCIStorageDir:     "/var/lib/charm-registry/oci",
	})

	assert.Equal(t, "filesystem", storage.Driver)
	assert.Equal(t, "/var/lib/charm-registry/oci", storage.Params["rootdirectory"])
}

func TestSyncPackageCredentialsAreStableForSamePackageID(t *testing.T) {
	t.Parallel()

	client := testClient(repo.NewMemory())
	first, err := client.SyncPackage(context.Background(), core.Package{ID: "pkg-1", Name: "demo"})
	require.NoError(t, err)
	second, err := client.SyncPackage(context.Background(), core.Package{ID: "pkg-1", Name: "demo"})
	require.NoError(t, err)

	firstPullUser, firstPullPassword, err := client.Credentials(first, true)
	require.NoError(t, err)
	secondPullUser, secondPullPassword, err := client.Credentials(second, true)
	require.NoError(t, err)
	firstPushUser, firstPushPassword, err := client.Credentials(first, false)
	require.NoError(t, err)
	secondPushUser, secondPushPassword, err := client.Credentials(second, false)
	require.NoError(t, err)

	assert.Equal(t, first.OCIPullRobot.ID, second.OCIPullRobot.ID)
	assert.Equal(t, firstPullUser, secondPullUser)
	assert.Equal(t, firstPullPassword, secondPullPassword)
	assert.Equal(t, first.OCIPushRobot.ID, second.OCIPushRobot.ID)
	assert.Equal(t, firstPushUser, secondPushUser)
	assert.Equal(t, firstPushPassword, secondPushPassword)
}

func TestAuthorizedAllowsExpectedCredentialScopes(t *testing.T) {
	client := testClient(repo.NewMemory())
	pkg, err := client.SyncPackage(context.Background(), core.Package{ID: "pkg-1", Name: "demo"})
	require.NoError(t, err)
	pushUser, pushPassword, err := client.Credentials(pkg, false)
	require.NoError(t, err)
	pullUser, pullPassword, err := client.Credentials(pkg, true)
	require.NoError(t, err)

	// Act + Assert
	assert.True(t, client.authorized(pkg, pushUser, pushPassword, true))
	assert.True(t, client.authorized(pkg, pushUser, pushPassword, false))
	assert.True(t, client.authorized(pkg, pullUser, pullPassword, false))
	assert.False(t, client.authorized(pkg, pullUser, pullPassword, true))
	assert.False(t, client.authorized(pkg, pullUser, "wrong", false))

}

func TestAuthMiddlewareEnforcesPackageScopedBasicAuth(t *testing.T) {
	memory := repo.NewMemory()
	client := testClient(memory)
	pkg, err := client.SyncPackage(context.Background(), core.Package{ID: "pkg-1", Name: "demo"})
	require.NoError(t, err)
	require.NoError(t, memory.CreatePackage(context.Background(), pkg))
	pullUser, pullPassword, err := client.Credentials(pkg, true)
	require.NoError(t, err)
	pushUser, pushPassword, err := client.Credentials(pkg, false)
	require.NoError(t, err)
	handler := client.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name       string
		method     string
		path       string
		username   string
		password   string
		wantStatus int
	}{
		{
			name:       "ping without auth is challenged",
			method:     http.MethodGet,
			path:       "/v2/",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "ping with auth is forwarded",
			method:     http.MethodGet,
			path:       "/v2/",
			username:   pullUser,
			password:   pullPassword,
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "missing auth is challenged",
			method:     http.MethodGet,
			path:       "/v2/charm-demo/app-image/manifests/latest",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "catalog is challenged",
			method:     http.MethodGet,
			path:       "/v2/_catalog",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "pull credential can read",
			method:     http.MethodGet,
			path:       "/v2/charm-demo/app-image/manifests/latest",
			username:   pullUser,
			password:   pullPassword,
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "pull credential cannot write",
			method:     http.MethodPut,
			path:       "/v2/charm-demo/app-image/manifests/latest",
			username:   pullUser,
			password:   pullPassword,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "push credential can write",
			method:     http.MethodPut,
			path:       "/v2/charm-demo/app-image/manifests/latest",
			username:   pushUser,
			password:   pushPassword,
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "cross package path is challenged",
			method:     http.MethodGet,
			path:       "/v2/charm-other/app-image/manifests/latest",
			username:   pushUser,
			password:   pushPassword,
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.username != "" {
				req.SetBasicAuth(tt.username, tt.password)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			assert.Equal(t, tt.wantStatus, recorder.Code)
			if tt.wantStatus == http.StatusUnauthorized {
				assert.Equal(t, `Basic realm="charm-registry-oci"`, recorder.Header().Get("WWW-Authenticate"))
			}
		})
	}

}

func TestRepositoryProjectExtractsProjectName(t *testing.T) {

	tests := []struct {
		path string
		want string
		ok   bool
	}{
		{path: "/v2/charm-demo/app-image/manifests/latest", want: "charm-demo", ok: true},
		{path: "/v2/charm-demo/app-image/blobs/uploads/", want: "charm-demo", ok: true},
		{path: "/v2/charm-demo/app-image/tags/list", want: "charm-demo", ok: true},
		{path: "/v2/", ok: false},
		{path: "/v2/charm-demo/app-image", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, ok := repositoryProject(tt.path)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}

}

func testClient(repository repo.Repository) *Client {
	return &Client{
		repository:       repository,
		publicRegistry:   "https://registry.example.com",
		internalRegistry: "http://127.0.0.1:5000",
		projectPrefix:    "charm",
		pullRobotPrefix:  "pull",
		pushRobotPrefix:  "push",
		secretKey:        func() []byte { k, _ := deriveKey("test-secret"); return k }(),
	}
}
