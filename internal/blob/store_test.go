package blob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockS3Client implements s3API for unit tests.
type mockS3Client struct {
	headBucketFn   func(ctx context.Context, input *s3.HeadBucketInput, opts ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
	createBucketFn func(ctx context.Context, input *s3.CreateBucketInput, opts ...func(*s3.Options)) (*s3.CreateBucketOutput, error)
	putObjectFn    func(ctx context.Context, input *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	getObjectFn    func(ctx context.Context, input *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	deleteObjectFn func(ctx context.Context, input *s3.DeleteObjectInput, opts ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

func (m *mockS3Client) HeadBucket(ctx context.Context, input *s3.HeadBucketInput, opts ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	return m.headBucketFn(ctx, input, opts...)
}

func (m *mockS3Client) CreateBucket(ctx context.Context, input *s3.CreateBucketInput, opts ...func(*s3.Options)) (*s3.CreateBucketOutput, error) {
	return m.createBucketFn(ctx, input, opts...)
}

func (m *mockS3Client) PutObject(ctx context.Context, input *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	return m.putObjectFn(ctx, input, opts...)
}

func (m *mockS3Client) GetObject(ctx context.Context, input *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	return m.getObjectFn(ctx, input, opts...)
}

func (m *mockS3Client) DeleteObject(ctx context.Context, input *s3.DeleteObjectInput, opts ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	return m.deleteObjectFn(ctx, input, opts...)
}

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

	// Arrange
	ctx := context.Background()
	store := NewMemoryStore()

	// Act
	_, err := store.Get(ctx, "nonexistent")

	// Assert
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

}

func TestMemoryStoreOverwrite(t *testing.T) {
	t.Parallel()

	// Arrange
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

	// Arrange
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

func TestMemoryStoreDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	require.NoError(t, store.Put(ctx, "key", []byte("hello"), "text/plain"))
	require.NoError(t, store.Delete(ctx, "key"))
	_, err := store.Get(ctx, "key")
	require.Error(t, err)
}

// ---- S3Store tests --------------------------------------------------------

func TestS3StoreEnsureBucketAlreadyExists(t *testing.T) {
	t.Parallel()

	// Arrange
	createCalled := false
	mock := &mockS3Client{
		headBucketFn: func(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
			return &s3.HeadBucketOutput{}, nil // bucket exists
		},
		createBucketFn: func(_ context.Context, _ *s3.CreateBucketInput, _ ...func(*s3.Options)) (*s3.CreateBucketOutput, error) {
			createCalled = true
			return nil, nil
		},
	}
	store := newS3StoreWithClient(mock, "my-bucket", "us-east-1", true)

	// Act
	err := store.ensureBucket(context.Background())

	// Assert
	require.NoError(t, err)
	assert.False(t, createCalled, "CreateBucket should not be called when HeadBucket succeeds")

}

func TestS3StoreEnsureBucketCreatedWithoutConstraint(t *testing.T) {
	t.Parallel()

	// Arrange
	// isS3=false → no LocationConstraint regardless of region.
	var capturedInput *s3.CreateBucketInput
	mock := &mockS3Client{
		headBucketFn: func(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
			return nil, errors.New("bucket not found")
		},
		createBucketFn: func(_ context.Context, input *s3.CreateBucketInput, _ ...func(*s3.Options)) (*s3.CreateBucketOutput, error) {
			capturedInput = input
			return &s3.CreateBucketOutput{}, nil
		},
	}
	store := newS3StoreWithClient(mock, "my-bucket", "eu-west-1", false /* isS3=false */)

	// Act
	err := store.ensureBucket(context.Background())

	// Assert
	require.NoError(t, err)
	require.NotNil(t, capturedInput)
	assert.Nil(t, capturedInput.CreateBucketConfiguration, "no LocationConstraint expected when isS3=false")

}

func TestS3StoreEnsureBucketCreatedWithRegionConstraint(t *testing.T) {
	t.Parallel()

	// Arrange
	// isS3=true, region != "us-east-1" → LocationConstraint must be set.
	var capturedInput *s3.CreateBucketInput
	mock := &mockS3Client{
		headBucketFn: func(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
			return nil, errors.New("bucket not found")
		},
		createBucketFn: func(_ context.Context, input *s3.CreateBucketInput, _ ...func(*s3.Options)) (*s3.CreateBucketOutput, error) {
			capturedInput = input
			return &s3.CreateBucketOutput{}, nil
		},
	}
	store := newS3StoreWithClient(mock, "my-bucket", "eu-west-1", true /* isS3=true */)

	// Act
	err := store.ensureBucket(context.Background())

	// Assert
	require.NoError(t, err)
	require.NotNil(t, capturedInput)
	require.NotNil(t, capturedInput.CreateBucketConfiguration)
	assert.Equal(t, "eu-west-1", string(capturedInput.CreateBucketConfiguration.LocationConstraint))

}

func TestS3StoreEnsureBucketNoConstraintForUsEast1(t *testing.T) {
	t.Parallel()

	// Arrange
	// isS3=true, region == "us-east-1" → LocationConstraint must NOT be set.
	var capturedInput *s3.CreateBucketInput
	mock := &mockS3Client{
		headBucketFn: func(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
			return nil, errors.New("bucket not found")
		},
		createBucketFn: func(_ context.Context, input *s3.CreateBucketInput, _ ...func(*s3.Options)) (*s3.CreateBucketOutput, error) {
			capturedInput = input
			return &s3.CreateBucketOutput{}, nil
		},
	}
	store := newS3StoreWithClient(mock, "my-bucket", "us-east-1", true /* isS3=true */)

	// Act
	err := store.ensureBucket(context.Background())

	// Assert
	require.NoError(t, err)
	require.NotNil(t, capturedInput)
	assert.Nil(t, capturedInput.CreateBucketConfiguration, "us-east-1 must not include a LocationConstraint")

}

func TestS3StoreEnsureBucketCreateError(t *testing.T) {
	t.Parallel()

	// Arrange
	createErr := errors.New("create failed")
	mock := &mockS3Client{
		headBucketFn: func(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
			return nil, errors.New("bucket not found")
		},
		createBucketFn: func(_ context.Context, _ *s3.CreateBucketInput, _ ...func(*s3.Options)) (*s3.CreateBucketOutput, error) {
			return nil, createErr
		},
	}
	store := newS3StoreWithClient(mock, "my-bucket", "us-east-1", true)

	// Act
	err := store.ensureBucket(context.Background())

	// Assert
	assert.ErrorIs(t, err, createErr)

}

func TestS3BaseEndpointKeepsTLSByDefault(t *testing.T) {
	t.Parallel()

	// Act + Assert

	assert.Equal(t, "https://s3.example.test", s3BaseEndpoint("https://s3.example.test", false))
}

func TestS3BaseEndpointDisablesTLS(t *testing.T) {
	t.Parallel()

	// Act + Assert

	assert.Equal(t, "http://s3.example.test", s3BaseEndpoint("https://s3.example.test", true))
	assert.Equal(t, "http://minio:9000", s3BaseEndpoint("minio:9000", true))
}

func TestS3StorePutSuccess(t *testing.T) {
	t.Parallel()

	// Arrange
	var capturedInput *s3.PutObjectInput
	mock := &mockS3Client{
		putObjectFn: func(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			capturedInput = input
			return &s3.PutObjectOutput{}, nil
		},
	}
	store := newS3StoreWithClient(mock, "my-bucket", "us-east-1", true)

	// Act
	err := store.Put(context.Background(), "charms/foo/1.charm", []byte("charm data"), "application/octet-stream")

	// Assert
	require.NoError(t, err)
	require.NotNil(t, capturedInput)
	assert.Equal(t, "my-bucket", *capturedInput.Bucket)
	assert.Equal(t, "charms/foo/1.charm", *capturedInput.Key)
	assert.Equal(t, "application/octet-stream", *capturedInput.ContentType)

	body, err := io.ReadAll(capturedInput.Body)
	require.NoError(t, err)
	assert.Equal(t, []byte("charm data"), body)

}

func TestS3StorePutError(t *testing.T) {
	t.Parallel()

	// Arrange
	putErr := errors.New("S3 write failed")
	mock := &mockS3Client{
		putObjectFn: func(_ context.Context, _ *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			return nil, putErr
		},
	}
	store := newS3StoreWithClient(mock, "my-bucket", "us-east-1", true)

	// Act
	err := store.Put(context.Background(), "key", []byte("data"), "text/plain")

	// Assert
	assert.ErrorIs(t, err, putErr)

}

func TestS3StoreGetSuccess(t *testing.T) {
	t.Parallel()

	// Arrange
	content := []byte("retrieved charm bytes")
	mock := &mockS3Client{
		getObjectFn: func(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			assert.Equal(t, "my-bucket", *input.Bucket)
			assert.Equal(t, "charms/foo/1.charm", *input.Key)
			return &s3.GetObjectOutput{
				Body: io.NopCloser(bytes.NewReader(content)),
			}, nil
		},
	}
	store := newS3StoreWithClient(mock, "my-bucket", "us-east-1", true)

	// Act
	data, err := store.Get(context.Background(), "charms/foo/1.charm")

	// Assert
	require.NoError(t, err)
	assert.Equal(t, content, data)

}

func TestS3StoreGetError(t *testing.T) {
	t.Parallel()

	// Arrange
	getErr := errors.New("S3 read failed")
	mock := &mockS3Client{
		getObjectFn: func(_ context.Context, _ *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			return nil, getErr
		},
	}
	store := newS3StoreWithClient(mock, "my-bucket", "us-east-1", true)

	// Act
	_, err := store.Get(context.Background(), "key")

	// Assert
	assert.ErrorIs(t, err, getErr)

}

func TestS3StoreDelete(t *testing.T) {
	t.Parallel()

	mock := &mockS3Client{
		deleteObjectFn: func(_ context.Context, input *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
			assert.Equal(t, "my-bucket", *input.Bucket)
			assert.Equal(t, "key", *input.Key)
			return &s3.DeleteObjectOutput{}, nil
		},
	}
	store := newS3StoreWithClient(mock, "my-bucket", "us-east-1", true)

	require.NoError(t, store.Delete(context.Background(), "key"))
}
