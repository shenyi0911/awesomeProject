package enum

type Category string

const (
	CategoryGeneral    Category = "通用"
	CategoryCollection Category = "采集"
	CategoryDecision   Category = "决策"
)

func IsValidCategory(c Category) bool {
	switch c {
	case CategoryGeneral, CategoryCollection, CategoryDecision:
		return true
	}
	return false
}
