package request

// UpdateVersionRequest is a request to update a version (drafts only).
type UpdateVersionRequest struct {
	Description   string `json:"description"`
	EntryPoint    string `json:"entryPoint"`
	ParameterSpec string `json:"parameterSpec"`
	ReleaseNote   string `json:"releaseNote"`
	Operator      string `json:"operator"`
}
