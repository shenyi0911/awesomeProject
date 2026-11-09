package enum

type VersionStatus string

const (
	VersionStatusDraft    VersionStatus = "draft"
	VersionStatusReleased VersionStatus = "released"
	VersionStatusArchived VersionStatus = "archived"
	VersionStatusRollback VersionStatus = "rollback"
)
