package entity

type DashboardStats struct {
	TotalAlgorithms  int            `json:"totalAlgorithms"`
	TotalVersions    int            `json:"totalVersions"`
	DraftVersions    int            `json:"draftVersions"`
	ReleasedVersions int            `json:"releasedVersions"`
	CategoryCounts   map[string]int `json:"categoryCounts"`
}
