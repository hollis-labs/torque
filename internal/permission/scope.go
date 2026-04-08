package permission

type Scope string

const (
	ScopeOnce    Scope = "once"
	ScopeSession Scope = "session"
	ScopeProject Scope = "project"
)
