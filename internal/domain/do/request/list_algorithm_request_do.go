package request

// ListAlgorithmRequest is a request to list algorithms.
type ListAlgorithmRequest struct {
	Name   string `form:"name"`
	Code   string `form:"code"`
	Limit  int    `form:"limit,default=20"`
	Offset int    `form:"offset,default=0"`
}
