package entity

type AlgorithmStats struct {
	TotalVersions    int `json:"totalVersions"`
	DraftVersions    int `json:"draftVersions"`
	ReleasedVersions int `json:"releasedVersions"`
}
