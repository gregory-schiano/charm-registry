package charm

import (
	"archive/zip"
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseArchive(t *testing.T) {
	t.Parallel()
	archive := buildZip(t, map[string]string{
		"metadata.yaml": "name: test-charm\nsummary: Test\ndescription: A test charm\n",
		"config.yaml":   "options: {}\n",
		"actions.yaml":  "restart: {}\n",
		"bundle.yaml":   "applications: {}\n",
		"README.md":     "# Test Charm\n",
	})
	result, err := ParseArchive(archive)
	require.NoError(t, err)
	assert.Equal(t, "test-charm", result.Manifest.Name)
	assert.Equal(t, "Test", result.Manifest.Summary)
	assert.Equal(t, "A test charm", result.Manifest.Description)
	assert.Equal(t, "options: {}\n", result.ConfigYAML)
	assert.Equal(t, "restart: {}\n", result.ActionsYAML)
	assert.Equal(t, "applications: {}\n", result.BundleYAML)
	assert.Equal(t, "# Test Charm\n", result.ReadmeMD)
	assert.NotNil(t, result.Manifest.Resources)
}

func TestParseArchiveInvalidZip(t *testing.T) {
	t.Parallel()
	_, err := ParseArchive([]byte("not a zip file"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "open charm archive")

}

func TestParseArchiveMissingMetadata(t *testing.T) {
	t.Parallel()
	archive := buildZip(t, map[string]string{
		"config.yaml": "options: {}\n",
	})
	_, err := ParseArchive(archive)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metadata.yaml not found")

}

func TestParseArchiveInvalidYAML(t *testing.T) {
	t.Parallel()
	archive := buildZip(t, map[string]string{
		"metadata.yaml": "name: [invalid\n",
	})
	_, err := ParseArchive(archive)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse metadata.yaml")

}

func TestParseArchiveRejectsOversizedZipEntry(t *testing.T) {
	t.Parallel()
	archive := buildZip(t, map[string]string{
		"metadata.yaml": "name: " + strings.Repeat("a", int(defaultMaxArchiveFileSize)) + "\n",
	})
	_, err := ParseArchive(archive)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `read charm archive entry: archive entry "metadata.yaml" exceeds the 10 MiB per-file safety limit`)

}

func TestParseArchiveFileReadsArchiveFromDisk(t *testing.T) {
	t.Parallel()

	payload := buildZip(t, map[string]string{
		"metadata.yaml": "name: disk-charm\nsummary: Disk\ndescription: Disk-backed parse\n",
	})
	path := t.TempDir() + "/disk-charm.charm"
	require.NoError(t, os.WriteFile(path, payload, 0o600))

	archive, err := ParseArchiveFile(path, int64(len(payload)), defaultMaxArchiveFileSize)

	require.NoError(t, err)
	assert.Equal(t, "disk-charm", archive.Manifest.Name)
	assert.Equal(t, "Disk", archive.Manifest.Summary)
}

func TestParseArchiveFileRejectsInvalidSize(t *testing.T) {
	t.Parallel()

	payload := buildZip(t, map[string]string{
		"metadata.yaml": "name: disk-charm\n",
	})
	path := t.TempDir() + "/disk-charm.charm"
	require.NoError(t, os.WriteFile(path, payload, 0o600))

	_, err := ParseArchiveFile(path, 0, defaultMaxArchiveFileSize)

	require.ErrorContains(t, err, "invalid charm archive size")
}

func TestParseArchiveWithCustomMaxFileSize(t *testing.T) {
	t.Parallel()

	archive := buildZip(t, map[string]string{
		"metadata.yaml": "name: " + strings.Repeat("a", 256) + "\n",
	})

	_, err := ParseArchiveWithMaxFileSize(archive, 128)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `read charm archive entry: archive entry "metadata.yaml" exceeds the 128 bytes per-file safety limit`)
}

func TestParseArchiveAutoGeneratesOCIResourcesFromContainers(t *testing.T) {
	t.Parallel()
	archive := buildZip(t, map[string]string{
		"metadata.yaml": "name: charm\ncontainers:\n  web:\n    resource: web-image\n",
	})
	result, err := ParseArchive(archive)
	require.NoError(t, err)
	resource, ok := result.Manifest.Resources["web-image"]
	require.True(t, ok, "auto-generated OCI resource should exist")
	assert.Equal(t, "oci-image", resource.Type)

}

func TestParseArchiveDoesNotOverwriteExistingResource(t *testing.T) {
	t.Parallel()
	archive := buildZip(t, map[string]string{
		"metadata.yaml": "name: charm\nresources:\n  web-image:\n    type: oci-image\n    description: custom desc\ncontainers:\n  web:\n    resource: web-image\n",
	})
	result, err := ParseArchive(archive)
	require.NoError(t, err)
	assert.Equal(t, "custom desc", result.Manifest.Resources["web-image"].Description)

}

func TestParseArchiveSkipsContainerWithoutResource(t *testing.T) {
	t.Parallel()
	archive := buildZip(t, map[string]string{
		"metadata.yaml": "name: charm\ncontainers:\n  sidecar:\n    resource: \"\"\n",
	})
	result, err := ParseArchive(archive)
	require.NoError(t, err)
	assert.Empty(t, result.Manifest.Resources)

}

func TestParseArchiveMinimalMetadata(t *testing.T) {
	t.Parallel()
	archive := buildZip(t, map[string]string{
		"metadata.yaml": "name: bare\n",
	})
	result, err := ParseArchive(archive)
	require.NoError(t, err)
	assert.Equal(t, "bare", result.Manifest.Name)
	assert.Empty(t, result.ConfigYAML)
	assert.Empty(t, result.ActionsYAML)
	assert.Empty(t, result.ReadmeMD)

}

func TestParseArchiveWithRelations(t *testing.T) {
	t.Parallel()
	archive := buildZip(t, map[string]string{
		"metadata.yaml": "name: charm\nprovides:\n  db:\n    interface: postgresql_client\nrequires:\n  ingress:\n    interface: ingress\npeers:\n  cluster:\n    interface: cluster\n",
	})
	result, err := ParseArchive(archive)
	require.NoError(t, err)
	assert.Contains(t, result.Manifest.Provides, "db")
	assert.Contains(t, result.Manifest.Requires, "ingress")
	assert.Contains(t, result.Manifest.Peers, "cluster")

}

func TestParseArchiveAcceptsDocumentedMetadataSchema(t *testing.T) {
	t.Parallel()
	archive := buildZip(t, map[string]string{
		"metadata.yaml": `name: super-k8s
summary: a really great charm
description: |
  This is a really great charm.
display-name: Super K8s
maintainers:
  - Joe Bloggs <joe.bloggs@example.com>
docs: https://discourse.charmhub.io/t/9999
source:
  - https://github.com/foo/super-k8s-operator
issues:
  - https://github.com/foo/super-k8s-operator/issues/
website:
  - https://charmed-super.io/k8s
  - https://super-app.io
terms:
  - super-terms
containers:
  super-app:
    resource: super-app-image
    mounts:
      - storage: logs
        location: /logs
    uid: 123
    gid: 124
  super-app-helper:
    bases:
      - name: ubuntu
        channel: "24.04"
        architectures:
          - amd64
          - arm64
resources:
  super-app-image:
    type: oci-image
    description: OCI image for the Super App
  definitions:
    type: file
    description: A small SQLite3 database of definitions needed by super app
    filename: definitions.db
provides:
  super-worker:
    interface: super-worker
    limit: 2
    optional: true
    scope: container
requires:
  ingress:
    interface: ingress
    optional: true
    limit: 1
peers:
  super-replicas:
    interface: super-replicas
peer:
  legacy-peer:
    interface: legacy-peer
storage:
  logs:
    type: filesystem
    location: /logs
    description: Storage mount for application logs
    read-only: true
    multiple: 1-3
    minimum-size: 1G
    properties:
      - transient
devices:
  gpu:
    type: nvidia.com/gpu
    description: GPU
    countmin: 1
    countmax: 2
extra-bindings:
  public:
assumes:
  - juju >= 3.6.0
  - any-of:
    - k8s-api
    - juju >= 3.6.0
charm-user: non-root
`,
	})

	result, err := ParseArchive(archive)

	require.NoError(t, err)
	assert.Equal(t, "super-k8s", result.Manifest.Name)
	assert.Equal(t, "Super K8s", result.Manifest.DisplayName)
	assert.Equal(t, []string{"Joe Bloggs <joe.bloggs@example.com>"}, result.Manifest.Maintainers)
	assert.Equal(t, []string{"https://discourse.charmhub.io/t/9999"}, []string(result.Manifest.Docs))
	assert.Equal(t, []string{"https://github.com/foo/super-k8s-operator"}, []string(result.Manifest.Source))
	assert.Equal(t, []string{"super-terms"}, result.Manifest.Terms)
	assert.Equal(t, "super-app-image", result.Manifest.Containers["super-app"].Resource)
	assert.Equal(t, "/logs", result.Manifest.Containers["super-app"].Mounts[0].Location)
	assert.Equal(t, 123, result.Manifest.Containers["super-app"].UID)
	assert.Equal(t, []string{"amd64", "arm64"}, result.Manifest.Containers["super-app-helper"].Bases[0].Architectures)
	assert.Equal(t, "definitions.db", result.Manifest.Resources["definitions"].Filename)
	assert.Equal(t, 2, result.Manifest.Provides["super-worker"].Limit)
	assert.True(t, result.Manifest.Provides["super-worker"].Optional)
	assert.Equal(t, "container", result.Manifest.Provides["super-worker"].Scope)
	assert.Equal(t, "legacy-peer", result.Manifest.Peers["legacy-peer"].Interface)
	assert.True(t, result.Manifest.Storage["logs"].ReadOnly)
	assert.Equal(t, "1-3", result.Manifest.Storage["logs"].Multiple)
	assert.Equal(t, "1G", result.Manifest.Storage["logs"].MinimumSize)
	assert.Equal(t, 2, result.Manifest.Devices["gpu"].CountMax)
	assert.Contains(t, result.Manifest.ExtraBindings, "public")
	assert.Equal(t, "non-root", result.Manifest.CharmUser)
}

func TestParseArchiveAcceptsListValuedLinks(t *testing.T) {
	t.Parallel()
	archive := buildZip(t, map[string]string{
		"metadata.yaml": `name: self-signed-certificates
summary: An operator to provide self-signed X.509 certificates to your charms.
issues:
- https://github.com/canonical/self-signed-certificates-operator/issues
source:
- https://github.com/canonical/self-signed-certificates-operator
website:
- https://charmhub.io/self-signed-certificates
provides:
  certificates:
    interface: tls-certificates
    optional: true
requires:
  tracing:
    interface: tracing
    limit: 1
    optional: true
`,
	})
	result, err := ParseArchive(archive)
	require.NoError(t, err)
	assert.Equal(t, "self-signed-certificates", result.Manifest.Name)
	assert.Equal(t, []string{"https://github.com/canonical/self-signed-certificates-operator/issues"}, []string(result.Manifest.Issues))
	assert.Equal(t, []string{"https://github.com/canonical/self-signed-certificates-operator"}, []string(result.Manifest.Source))
	assert.Equal(t, []string{"https://charmhub.io/self-signed-certificates"}, ExtractWebsites(result.Manifest.Website))
	assert.Equal(t, "tls-certificates", result.Manifest.Provides["certificates"].Interface)
}

func TestExtractWebsites(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    any
		expected []string
	}{
		{"string value", "https://example.com", []string{"https://example.com"}},
		{"empty string", "", nil},
		{"slice of any", []any{"https://a.com", "https://b.com"}, []string{"https://a.com", "https://b.com"}},
		{"slice of any filters empty", []any{"https://a.com", "", "https://b.com"}, []string{"https://a.com", "https://b.com"}},
		{"slice of any ignores non-string", []any{"https://a.com", 42}, []string{"https://a.com"}},
		{"slice of string", []string{"https://a.com"}, []string{"https://a.com"}},
		{"nil", nil, nil},
		{"unsupported type", 42, nil},
		{"empty slice of any", []any{}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ExtractWebsites(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}

}

func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, content := range files {
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entry.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buf.Bytes()
}
