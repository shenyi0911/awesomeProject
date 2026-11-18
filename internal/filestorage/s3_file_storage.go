package filestorage

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"awesomeProject/internal/domain/port"
)

// s3FileStorage implements file storage on an S3-compatible object store (currently RustFS;
// any S3-compatible backend works). It uses minio-go/v7, a generic S3 client independent of the backend.
type s3FileStorage struct {
	client *minio.Client
	bucket string
}

var _ port.FileStorage = (*s3FileStorage)(nil)

// bucketInitTimeout bounds bucket existence checks and creation during startup.
const bucketInitTimeout = 10 * time.Second

// NewS3FileStorage creates the S3 client and ensures the bucket exists.
func NewS3FileStorage(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*s3FileStorage, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("create s3 client failed: %w", err)
	}

	s := &s3FileStorage{client: client, bucket: bucket}

	ctx, cancel := context.WithTimeout(context.Background(), bucketInitTimeout)
	defer cancel()
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("check bucket %s failed: %w", bucket, err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("make bucket %s failed: %w", bucket, err)
		}
	}
	return s, nil
}

func (s *s3FileStorage) Upload(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
	if _, err := s.client.PutObject(ctx, s.bucket, key, reader, size, minio.PutObjectOptions{
		ContentType: contentType,
	}); err != nil {
		return fmt.Errorf("s3 put object failed: %w", err)
	}
	return nil
}

func (s *s3FileStorage) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("s3 get object failed: %w", err)
	}
	// Probe existence: GetObject is lazy, only Stat reports a missing object.
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		return nil, fmt.Errorf("s3 object not found: %w", err)
	}
	return obj, nil
}

func (s *s3FileStorage) Delete(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("s3 remove object failed: %w", err)
	}
	return nil
}

func (s *s3FileStorage) PresignURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	u, err := s.client.PresignedGetObject(ctx, s.bucket, key, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("s3 presign failed: %w", err)
	}
	return u.String(), nil
}

// Health verifies that the object store is reachable and the configured bucket exists.
func (s *s3FileStorage) Health(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("s3 bucket check failed: %w", err)
	}
	if !exists {
		return fmt.Errorf("s3 bucket %s not found", s.bucket)
	}
	return nil
}
