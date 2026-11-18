package entity

type AlgorithmInfo struct {
	Algorithm Algorithm          `json:"algorithm"`
	Versions  []AlgorithmVersion `json:"versions"`
	Stats     AlgorithmStats     `json:"stats"`
}
