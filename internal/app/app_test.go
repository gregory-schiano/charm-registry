package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gschiano/charm-registry/internal/config"
)

func TestNewWrapsBlobStoreErrors(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act
	_, err := New(ctx, config.Config{
		S3Bucket:          "test-bucket",
		S3Region:          "us-east-1",
		S3AccessKeyID:     "test-access-key",
		S3SecretAccessKey: "test-secret-key",
	})

	// Assert
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot create blob store")

}
