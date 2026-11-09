package entity

type AlgorithmFile struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	Type        string `json:"type"`
	ContentType string `json:"contentType"` // MIME 类型（如 application/pdf），下载 MinIO 对象时使用
}
