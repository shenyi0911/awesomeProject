package entity

type AlgorithmFile struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	Type        string `json:"type"`
	ContentType string `json:"contentType"` // MIME type (e.g. application/pdf) used when downloading from the S3-compatible store.
}
