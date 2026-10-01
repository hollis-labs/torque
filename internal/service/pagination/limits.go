package pagination

// DefaultLimit and MaxLimit are the public list page-size policy. They
// bound a page, never the number of rows a cursor can traverse.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)
