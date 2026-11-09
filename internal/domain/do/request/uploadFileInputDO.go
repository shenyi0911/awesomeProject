package request

import "io"

// UploadFileInput 文件上传输入（由 handler 从 multipart 解析后传入，避免 service 依赖 gin）。
type UploadFileInput struct {
	Reader       io.Reader
	Size         int64
	Filename     string
	ContentType  string
	RelativePath string // 文件夹上传时的相对路径（含子目录），为空则退化为单文件名
}
