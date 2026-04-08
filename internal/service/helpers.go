package service

import "encoding/json"

func marshalJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
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
