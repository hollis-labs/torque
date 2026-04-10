package service

import "encoding/json"

func marshalJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// unmarshalJSON is a thin wrapper around json.Unmarshal for symmetry with
// marshalJSON. Returns the underlying error so callers can decide whether
// to surface or silently fall back.
func unmarshalJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func orDefault(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}

func orDefaultInt(val *int, fallback int) int {
	if val != nil {
		return *val
	}
	return fallback
}
