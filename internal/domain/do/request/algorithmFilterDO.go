package request

// AlgorithmFilter 算法查询过滤条件
type AlgorithmFilter struct {
	Name   string
	Code   string
	Limit  int
	Offset int
}
