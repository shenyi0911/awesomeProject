package request

// AlgorithmFilter filters algorithm queries.
type AlgorithmFilter struct {
	Name   string
	Code   string
	Limit  int
	Offset int
}
