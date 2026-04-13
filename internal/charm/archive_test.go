package charm

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseArchive(t *testing.T) {
	t.Parallel()

	// Arrange
	archive := buildZip(t, map[string]string{
		"metadata.yaml": "name: test-charm\nsummary: Test\ndescription: A test charm\n",
		"config.yaml":   "options: {}\n",
		"actions.yaml":  "restart: {}\n",
		"bundle.yaml":   "applications: {}\n",
		"README.md":     "# Test Charm\n",
	})

	// Act
	result, err := ParseArchive(archive)

	// Assert
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

	// Act
	_, err := ParseArchive([]byte("not a zip file"))

	// Assert
	require.Error(t, err)
	assert.Contains(t, err.Error(), "open charm archive")

}

func TestParseArchiveMissingMetadata(t *testing.T) {
	t.Parallel()

	// Arrange
	archive := buildZip(t, map[string]string{
		"config.yaml": "options: {}\n",
	})

	// Act
	_, err := ParseArchive(archive)

	// Assert
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metadata.yaml not found")

}

func TestParseArchiveInvalidYAML(t *testing.T) {
	t.Parallel()

	// Arrange
	archive := buildZip(t, map[string]string{
		"metadata.yaml": "name: [invalid\n",
	})

	// Act
	_, err := ParseArchive(archive)

	// Assert
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse metadata.yaml")

}

func TestParseArchiveRejectsOversizedZipEntry(t *testing.T) {
	t.Parallel()

	// Arrange
	archive := buildZip(t, map[string]string{
		"metadata.yaml": "name: " + strings.Repeat("a", maxArchiveFileSize) + "\n",
	})

	// Act
	_, err := ParseArchive(archive)

	// Assert
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metadata.yaml exceeds")

}

func TestParseArchiveAutoGeneratesOCIResourcesFromContainers(t *testing.T) {
	t.Parallel()

	// Arrange
	archive := buildZip(t, map[string]string{
		"metadata.yaml": "name: charm\ncontainers:\n  web:\n    resource: web-image\n",
	})

	// Act
	result, err := ParseArchive(archive)

	// Assert
	require.NoError(t, err)
	resource, ok := result.Manifest.Resources["web-image"]
	require.True(t, ok, "auto-generated OCI resource should exist")
	assert.Equal(t, "oci-image", resource.Type)

}

func TestParseArchiveDoesNotOverwriteExistingResource(t *testing.T) {
	t.Parallel()

	// Arrange
	archive := buildZip(t, map[string]string{
		"metadata.yaml": "name: charm\nresources:\n  web-image:\n    type: oci-image\n    description: custom desc\ncontainers:\n  web:\n    resource: web-image\n",
	})

	// Act
	result, err := ParseArchive(archive)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "custom desc", result.Manifest.Resources["web-image"].Description)

}

func TestParseArchiveSkipsContainerWithoutResource(t *testing.T) {
	t.Parallel()

	// Arrange
	archive := buildZip(t, map[string]string{
		"metadata.yaml": "name: charm\ncontainers:\n  sidecar:\n    resource: \"\"\n",
	})

	// Act
	result, err := ParseArchive(archive)

	// Assert
	require.NoError(t, err)
	assert.Empty(t, result.Manifest.Resources)

}

func TestParseArchiveMinimalMetadata(t *testing.T) {
	t.Parallel()

	// Arrange
	archive := buildZip(t, map[string]string{
		"metadata.yaml": "name: bare\n",
	})

	// Act
	result, err := ParseArchive(archive)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "bare", result.Manifest.Name)
	assert.Empty(t, result.ConfigYAML)
	assert.Empty(t, result.ActionsYAML)
	assert.Empty(t, result.ReadmeMD)

}

func TestParseArchiveWithRelations(t *testing.T) {
	t.Parallel()

	// Arrange
	archive := buildZip(t, map[string]string{
		"metadata.yaml": "name: charm\nprovides:\n  db:\n    interface: postgresql_client\nrequires:\n  ingress:\n    interface: ingress\npeers:\n  cluster:\n    interface: cluster\n",
	})

	// Act
	result, err := ParseArchive(archive)

	// Assert
	require.NoError(t, err)
	assert.Contains(t, result.Manifest.Provides, "db")
	assert.Contains(t, result.Manifest.Requires, "ingress")
	assert.Contains(t, result.Manifest.Peers, "cluster")

}

func TestExtractWebsites(t *testing.T) {
	t.Parallel()

	// Act
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

	// Assert
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
