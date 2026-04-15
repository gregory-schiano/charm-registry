package blob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/gschiano/charm-registry/internal/config"
)

type Store interface {
	Put(ctx context.Context, key string, payload []byte, contentType string) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}

type MemoryStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMemoryStore returns an in-memory blob store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: map[string][]byte{}}
}

// Put is part of the [Store] interface.
func (s *MemoryStore) Put(_ context.Context, key string, payload []byte, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = append([]byte(nil), payload...)
	return nil
}

// Get is part of the [Store] interface.
func (s *MemoryStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	payload, ok := s.data[key]
	if !ok {
		return nil, fmt.Errorf("blob %q not found", key)
	}
	return append([]byte(nil), payload...), nil
}

// Delete is part of the [Store] interface.
func (s *MemoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

// s3API is the subset of [s3.Client] methods used by [S3Store].
// Declared as an interface so tests can substitute a mock without a live AWS endpoint.
type s3API interface {
	HeadBucket(ctx context.Context, input *s3.HeadBucketInput, opts ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
	CreateBucket(ctx context.Context, input *s3.CreateBucketInput, opts ...func(*s3.Options)) (*s3.CreateBucketOutput, error)
	PutObject(ctx context.Context, input *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, input *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(ctx context.Context, input *s3.DeleteObjectInput, opts ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

type S3Store struct {
	client    s3API
	bucket    string
	region    string
	isS3      bool
	transport *http.Transport
}

// newS3StoreWithClient constructs an [S3Store] from an already-initialised client.
// It does NOT call [S3Store.ensureBucket]; callers are responsible for that.
// This function exists primarily to enable unit tests to inject a mock client.
func newS3StoreWithClient(client s3API, bucket, region string, isS3 bool) *S3Store {
	return &S3Store{
		client: client,
		bucket: bucket,
		region: region,
		isS3:   isS3,
	}
}

// NewS3Store builds an S3-backed blob store and ensures its bucket exists.
//
// The following errors may be returned:
// - Errors from loading the AWS SDK configuration.
// - Errors from checking or creating the configured bucket.
func NewS3Store(ctx context.Context, cfg config.Config) (*S3Store, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	httpClient := &http.Client{Transport: transport}
	loadOptions := []func(*awscfg.LoadOptions) error{
		awscfg.WithRegion(cfg.S3Region),
		awscfg.WithHTTPClient(httpClient),
	}
	if cfg.S3AccessKeyID != "" || cfg.S3SecretAccessKey != "" {
		loadOptions = append(loadOptions, awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.S3AccessKeyID,
			cfg.S3SecretAccessKey,
			"",
		)))
	}
	if cfg.S3Endpoint != "" {
		loadOptions = append(loadOptions, awscfg.WithBaseEndpoint(s3BaseEndpoint(cfg.S3Endpoint, cfg.S3DisableTLS)))
	}
	awsConfig, err := awscfg.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(awsConfig, func(o *s3.Options) {
		o.UsePathStyle = cfg.S3UsePathStyle
	})

	store := newS3StoreWithClient(client, cfg.S3Bucket, cfg.S3Region, cfg.S3Endpoint == "")
	store.transport = transport
	if err := store.ensureBucket(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *S3Store) ensureBucket(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &s.bucket})
	if err == nil {
		return nil
	}
	if isBucketAccessDenied(err) {
		return nil
	}
	input := &s3.CreateBucketInput{Bucket: &s.bucket}
	if s.isS3 && s.region != "" && s.region != "us-east-1" {
		input.CreateBucketConfiguration = &types.CreateBucketConfiguration{
			LocationConstraint: types.BucketLocationConstraint(s.region),
		}
	}
	_, err = s.client.CreateBucket(ctx, input)
	if isBucketAccessDenied(err) {
		return nil
	}
	return err
}

func isBucketAccessDenied(err error) bool {
	if err == nil {
		return false
	}
	var apiErr interface{ ErrorCode() string }
	if errors.As(err, &apiErr) && apiErr.ErrorCode() == "AccessDenied" {
		return true
	}
	return strings.Contains(err.Error(), "AccessDenied") || strings.Contains(err.Error(), "StatusCode: 403")
}

// Put is part of the [Store] interface.
func (s *S3Store) Put(ctx context.Context, key string, payload []byte, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &s.bucket,
		Key:         &key,
		Body:        bytes.NewReader(payload),
		ContentType: &contentType,
	})
	return err
}

// Get is part of the [Store] interface.
func (s *S3Store) Get(ctx context.Context, key string) ([]byte, error) {
	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// Delete is part of the [Store] interface.
func (s *S3Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	})
	return err
}

// Close releases idle HTTP connections held by the S3 client transport.
func (s *S3Store) Close() error {
	if s.transport != nil {
		s.transport.CloseIdleConnections()
	}
	return nil
}

func s3BaseEndpoint(endpoint string, disableTLS bool) string {
	if endpoint == "" {
		return ""
	}
	if !strings.Contains(endpoint, "://") {
		if disableTLS {
			return "http://" + endpoint
		}
		return "https://" + endpoint
	}
	if !disableTLS {
		return endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	if parsed.Scheme == "" {
		return "http://" + endpoint
	}
	parsed.Scheme = "http"
	return parsed.String()
}
