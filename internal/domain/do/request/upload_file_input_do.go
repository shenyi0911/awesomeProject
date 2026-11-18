package request

import "io"

// UploadFileInput is a file upload input parsed from multipart by the handler, keeping the service decoupled from gin.
type UploadFileInput struct {
	Reader       io.Reader
	Size         int64
	Filename     string
	ContentType  string
	RelativePath string // Relative path (including subdirectories) for folder uploads; falls back to the file name when empty.
}
