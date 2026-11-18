package utils

// Paging defaults shared by HTTP handlers and domain services.
const (
	defaultPageSize = 20
	maxPageSize     = 200
)

// NormalizePaging clamps paging parameters to the agreed range (page size is capped at maxPageSize).
func NormalizePaging(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
