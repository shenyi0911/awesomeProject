package entity

import "time"

// VersionChangeLog records a version change for audit and traceability,
// including the version, operator, change time, description, and file MD5 (when applicable).
type VersionChangeLog struct {
	ID          string    `json:"id"`
	AlgorithmID string    `json:"algorithmId"`
	VersionID   string    `json:"versionId"`
	Version     string    `json:"version"`
	Operator    string    `json:"operator"`
	Action      string    `json:"action"`
	ChangeTime  time.Time `json:"changeTime"`
	ChangeDesc  string    `json:"changeDesc"`
	FileMD5     string    `json:"fileMd5"` // File MD5, set only for file-related actions.
}
