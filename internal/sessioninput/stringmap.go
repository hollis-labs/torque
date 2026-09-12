package sessioninput

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// DecodeStringMapJSON decodes a session boundary string map from either a JSON
// object of string values or a legacy JSON string containing that object.
func DecodeStringMapJSON(raw json.RawMessage, field string) (map[string]string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("%s must be a JSON object of string values", field)
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if asString == "" {
			return nil, fmt.Errorf("%s must be a JSON object of string values", field)
		}
		raw = json.RawMessage(asString)
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("%s must be a JSON object of string values", field)
	}
	return rawStringMap(values, field)
}

// DecodeStringMapValue decodes an MCP argument that may already be materialized
// as a native object, or may still be the legacy JSON-object string shape.
func DecodeStringMapValue(v any, field string) (map[string]string, error) {
	switch t := v.(type) {
	case nil:
		return nil, fmt.Errorf("%s must be a JSON object of string values", field)
	case string:
		return DecodeStringMapJSON(json.RawMessage(t), field)
	case map[string]string:
		if len(t) == 0 {
			return map[string]string{}, nil
		}
		out := make(map[string]string, len(t))
		for k, v := range t {
			out[k] = v
		}
		return out, nil
	case map[string]any:
		out := make(map[string]string, len(t))
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			s, ok := t[k].(string)
			if !ok {
				return nil, fmt.Errorf("%s.%s must be a string", field, k)
			}
			out[k] = s
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s must be a JSON object of string values", field)
	}
}

func rawStringMap(values map[string]json.RawMessage, field string) (map[string]string, error) {
	if values == nil {
		return nil, fmt.Errorf("%s must be a JSON object of string values", field)
	}
	out := make(map[string]string, len(values))
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if bytes.Equal(bytes.TrimSpace(values[k]), []byte("null")) {
			return nil, fmt.Errorf("%s.%s must be a string", field, k)
		}
		var s *string
		if err := json.Unmarshal(values[k], &s); err != nil || s == nil {
			return nil, fmt.Errorf("%s.%s must be a string", field, k)
		}
		out[k] = *s
	}
	return out, nil
}
