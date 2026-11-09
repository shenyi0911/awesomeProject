package request

// UpdateVersionRequest 更新版本请求（仅草稿可改）
type UpdateVersionRequest struct {
	Description   string `json:"description"`
	EntryPoint    string `json:"entryPoint"`
	ParameterSpec string `json:"parameterSpec"`
	ReleaseNote   string `json:"releaseNote"`
	Operator      string `json:"operator"`
}
