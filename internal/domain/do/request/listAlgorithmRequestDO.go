package request

// ListAlgorithmRequest 查询算法列表请求
type ListAlgorithmRequest struct {
	Name   string `form:"name"`
	Code   string `form:"code"`
	Limit  int    `form:"limit,default=20"`
	Offset int    `form:"offset,default=0"`
}
