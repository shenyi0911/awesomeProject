package request

// UpdateAlgorithmRequest 更新算法请求（仅允许修改以下字段）
type UpdateAlgorithmRequest struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Scene       string `json:"scene"`
	UpdateUser  string `json:"updateUser"`
	Description string `json:"description"`
}
