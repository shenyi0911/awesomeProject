package utils

// NormalizeLimit / NormalizeOffset 分页参数归一化（各 SQL 仓储共用；上限 200 与前端约定一致）。
func NormalizeLimit(n int) int {
	if n <= 0 {
		return 20
	}
	if n > 200 {
		return 200
	}
	return n
}

func NormalizeOffset(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
