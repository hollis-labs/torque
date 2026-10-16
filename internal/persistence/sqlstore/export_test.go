package sqlstore

// Test-only access to the in-transaction event writer that T3 (self-report)
// will call from inside this package.
type (
	EventSpec  = eventSpec
	StateScope = stateScope
)

func AppendEvent(w *WriteTx, sp eventSpec) (string, error) { return w.appendEvent(sp) }
