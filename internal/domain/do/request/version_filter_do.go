package request

import "awesomeProject/internal/domain/enum"

// VersionFilter filters version queries.
type VersionFilter struct {
	Version string
	Status  enum.VersionStatus
	Limit   int
	Offset  int
}
