package request

// UpdateAlgorithmRequest is a request to update an algorithm. Only the fields below can be changed.
type UpdateAlgorithmRequest struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Scene       string `json:"scene"`
	UpdateUser  string `json:"updateUser"`
	Description string `json:"description"`
}
