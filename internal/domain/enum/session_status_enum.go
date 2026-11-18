package enum

// Session statuses.
const (
	// StatusActive marks a valid session.
	StatusActive = "active"
	// StatusClosed marks a session closed by the client; it no longer accepts requests.
	StatusClosed = "closed"
)
