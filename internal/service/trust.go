package service

// ResolveTrust returns the default trust level for a task given its source.
// MVP: deterministic map keyed on source_type; full implementation in Task A4.
// A known-source registry (BLG-035) will later override this per
// (source_type, source_ref).
//
// Stub — always returns "normal". Replaced in A4.
func ResolveTrust(sourceType, sourceRef string) string {
	return "normal"
}
