package request

// CreateVersionRequest is a request to create a version. When Version is empty,
// the backend generates v1, v2, ... per algorithm.
type CreateVersionRequest struct {
	AlgorithmID   string `json:"algorithmId" binding:"required"`
	Version       string `json:"version"`
	Description   string `json:"description"`
	EntryPoint    string `json:"entryPoint"`
	ParameterSpec string `json:"parameterSpec"`
	ReleaseNote   string `json:"releaseNote"`
	Operator      string `json:"operator"`
}
