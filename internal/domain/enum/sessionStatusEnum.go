package enum

// 会话状态（枚举）
const (
	// StatusActive 会话有效中。
	StatusActive = "active"
	// StatusClosed 会话已被主动关闭（不再接受请求）。
	StatusClosed = "closed"
)
