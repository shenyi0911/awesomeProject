package request

// RegisterAlgorithmRequest is a request to register an algorithm.
type RegisterAlgorithmRequest struct {
	Name        string `json:"name" binding:"required"`     // Algorithm name.
	Code        string `json:"code" binding:"required"`     // Globally unique algorithm code (letters and digits only).
	Description string `json:"description"`                 // Algorithm description.
	Category    string `json:"category" binding:"required"` // Algorithm category.
	Author      string `json:"author" binding:"required"`   // Algorithm author.
	Language    string `json:"language" binding:"required"` // Development language (enum: Java/Python/C++/C#/Go).
	Scene       string `json:"scene"`                       // Application scenario.
	Stateful    bool   `json:"stateful"`                    // Whether the algorithm is stateful.
}
