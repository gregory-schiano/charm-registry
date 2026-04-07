package blob

import (
	"bytes"
	"context"
	"fmt"
	"io"
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

type S3Store struct {
	client *s3.Client
	bucket string
	region string
	isS3   bool
}

// NewS3Store builds an S3-backed blob store and ensures its bucket exists.
//
// The following errors may be returned:
// - Errors from loading the AWS SDK configuration.
// - Errors from checking or creating the configured bucket.
func NewS3Store(ctx context.Context, cfg config.Config) (*S3Store, error) {
	loadOptions := []func(*awscfg.LoadOptions) error{
		awscfg.WithRegion(cfg.S3Region),
	}
	if cfg.S3AccessKeyID != "" || cfg.S3SecretAccessKey != "" {
		loadOptions = append(loadOptions, awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.S3AccessKeyID,
			cfg.S3SecretAccessKey,
			"",
		)))
	}
	if cfg.S3Endpoint != "" {
		loadOptions = append(loadOptions, awscfg.WithBaseEndpoint(cfg.S3Endpoint))
	}
	awsConfig, err := awscfg.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(awsConfig, func(o *s3.Options) {
		o.UsePathStyle = cfg.S3UsePathStyle
	})

	store := &S3Store{
		client: client,
		bucket: cfg.S3Bucket,
		region: cfg.S3Region,
		isS3:   cfg.S3Endpoint == "",
	}
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
	input := &s3.CreateBucketInput{Bucket: &s.bucket}
	if s.isS3 && s.region != "" && s.region != "us-east-1" {
		input.CreateBucketConfiguration = &types.CreateBucketConfiguration{
			LocationConstraint: types.BucketLocationConstraint(s.region),
		}
	}
	_, err = s.client.CreateBucket(ctx, input)
	return err
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
