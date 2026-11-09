package filestorage

import (
	"context"
	"io"
	"time"
)

// FileStorage 文件存储抽象：算法文件字节存放于 S3 兼容对象存储（当前后端 RustFS）或本地目录（memory）。
// 业务层只依赖该接口，具体实现由 config 决定，与 state 仓储的 memory/dapr 切换套路一致。
type FileStorage interface {
	// Upload 上传文件字节，key 为对象标识（如 algorithms/{algoId}/{uuid}-{name}）
	Upload(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error
	// Download 下载文件，返回可读流（调用方负责关闭）
	Download(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete 删除对象
	Delete(ctx context.Context, key string) error
	// PresignURL 生成可访问的下载地址：
	//   - s3 实现返回真实预签名 URL（前端直连 S3 后端，如 RustFS）
	//   - memory 实现返回后端代理路由（前端经 Go 流式取回）
	PresignURL(ctx context.Context, key string, expiry time.Duration) (string, error)
}
