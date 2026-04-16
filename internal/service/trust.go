package service

// ResolveTrust returns the default trust level for a task given its source.
// MVP: deterministic map keyed on source_type. A known-source registry
// (BLG-035) will later override this per (source_type, source_ref).
func ResolveTrust(sourceType, sourceRef string) string {
	switch sourceType {
	case "system":
		return "trusted"
	case "user", "agent", "api":
		return "normal"
	case "webhook", "import":
		return "untrusted"
	default:
		return "normal"
	}
}
