package entity

import (
	"time"

	"awesomeProject/internal/domain/enum"
)

// AlgorithmVersion is a version of an algorithm.
// Status flow: draft -> released -> archived.
// Each version owns an independent set of files; files of older versions are retained for rollback.
type AlgorithmVersion struct {
	ID            string             `json:"id"`
	AlgorithmID   string             `json:"algorithmId"`
	Version       string             `json:"version"`
	Description   string             `json:"description"`
	Status        enum.VersionStatus `json:"status"`
	IsActive      bool               `json:"isActive"`
	EntryPoint    string             `json:"entryPoint"`
	ParameterSpec string             `json:"parameterSpec"`
	ReleaseNote   string             `json:"releaseNote"`
	Files         []AlgorithmFile    `json:"files"`
	// Revision is an optimistic-lock counter used to detect concurrent file modifications.
	Revision  int64     `json:"-"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
