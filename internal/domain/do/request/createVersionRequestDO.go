package request

// CreateVersionRequest 创建版本请求。Version 为空时后端自动按算法维度生成 v1、v2...
type CreateVersionRequest struct {
	AlgorithmID   string `json:"algorithmId" binding:"required"`
	Version       string `json:"version"`
	Description   string `json:"description"`
	EntryPoint    string `json:"entryPoint"`
	ParameterSpec string `json:"parameterSpec"`
	ReleaseNote   string `json:"releaseNote"`
	Operator      string `json:"operator"`
}
