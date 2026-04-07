package blob

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryStorePutAndGet(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	store := NewMemoryStore()

	// Act
	err := store.Put(ctx, "key1", []byte("hello"), "text/plain")
	require.NoError(t, err)
	data, err := store.Get(ctx, "key1")

	// Assert
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), data)
}

func TestMemoryStoreGetNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	_, err := store.Get(ctx, "nonexistent")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestMemoryStoreOverwrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	// Act
	_ = store.Put(ctx, "key", []byte("v1"), "text/plain")
	_ = store.Put(ctx, "key", []byte("v2"), "text/plain")
	data, err := store.Get(ctx, "key")

	// Assert
	require.NoError(t, err)
	assert.Equal(t, []byte("v2"), data)
}

func TestMemoryStoreIsolatesCopies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	// Arrange: store data
	original := []byte("original")
	_ = store.Put(ctx, "key", original, "text/plain")

	// Act: mutate the original slice
	original[0] = 'x'
	data, _ := store.Get(ctx, "key")

	// Assert: store holds an independent copy
	assert.Equal(t, byte('o'), data[0], "Put should copy the input")

	// Act: mutate the retrieved slice
	data[0] = 'y'
	data2, _ := store.Get(ctx, "key")

	// Assert: subsequent Gets return independent copies
	assert.Equal(t, byte('o'), data2[0], "Get should return a copy")
}

func TestMemoryStoreMultipleKeys(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	// Act
	_ = store.Put(ctx, "a", []byte("alpha"), "text/plain")
	_ = store.Put(ctx, "b", []byte("beta"), "text/plain")

	// Assert
	a, _ := store.Get(ctx, "a")
	b, _ := store.Get(ctx, "b")
	assert.Equal(t, []byte("alpha"), a)
	assert.Equal(t, []byte("beta"), b)
}
