package filestorage

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// s3FileStorage S3 兼容对象存储实现（当前后端为 RustFS，可平滑替换为任意 S3 兼容服务）。
// 底层复用 minio-go/v7 —— 它是通用 S3 客户端，与具体后端（MinIO/RustFS/SeaweedFS）无关，
// 因此「换后端」无需改代码，只改 config 的 endpoint/凭据即可。
type s3FileStorage struct {
	client *minio.Client
	bucket string
}

// NewS3FileStorage 创建 S3 客户端并确保桶存在。
// endpoint 形如 127.0.0.1:9000（RustFS 默认端口）；accessKey/secretKey 需与后端启动凭据一致。
func NewS3FileStorage(endpoint, accessKey, secretKey, bucket string, useSSL bool) (FileStorage, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("create s3 client failed: %w", err)
	}

	s := &s3FileStorage{client: client, bucket: bucket}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	// 探测对象是否真实存在（GetObject 惰性，Stat 才会报错）
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
