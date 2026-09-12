package sessioninput

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeStringMapValue_StrictShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   any
		want map[string]string
	}{
		{name: "native object", in: map[string]any{"A": "B"}, want: map[string]string{"A": "B"}},
		{name: "native string map", in: map[string]string{"A": "B"}, want: map[string]string{"A": "B"}},
		{name: "JSON object string", in: `{"A":"B"}`, want: map[string]string{"A": "B"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeStringMapValue(tc.in, "env")
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	for _, tc := range []struct {
		name string
		in   any
	}{
		{name: "native null member", in: map[string]any{"A": nil}},
		{name: "native numeric member", in: map[string]any{"A": 1}},
		{name: "native boolean member", in: map[string]any{"A": true}},
		{name: "JSON null member", in: `{"A":null}`},
		{name: "JSON numeric member", in: `{"A":1}`},
		{name: "JSON boolean member", in: `{"A":true}`},
		{name: "array", in: []any{"A"}},
		{name: "scalar number", in: 1},
		{name: "empty string", in: ""},
		{name: "whitespace string", in: " \n\t "},
		{name: "trailing data", in: `{"A":"B"} {"C":"D"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeStringMapValue(tc.in, "env")
			require.Error(t, err)
		})
	}
}

func TestDecodeStringMapJSON_StrictShapes(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"A":null}`),
		json.RawMessage(`{"A":1}`),
		json.RawMessage(`{"A":true}`),
		json.RawMessage(`[]`),
		json.RawMessage(`1`),
		json.RawMessage(``),
		json.RawMessage(`   `),
		json.RawMessage(`{"A":"B"} {"C":"D"}`),
		json.RawMessage(`"{\"A\":null}"`),
		json.RawMessage(`"{\"A\":1}"`),
		json.RawMessage(`"{\"A\":false}"`),
	} {
		t.Run(string(raw), func(t *testing.T) {
			_, err := DecodeStringMapJSON(raw, "meta")
			require.Error(t, err)
		})
	}
}
