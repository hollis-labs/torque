package pagination_test

import (
	"encoding/json"
	"testing"

	"github.com/hollis-labs/torque/internal/service/pagination"
	"github.com/stretchr/testify/require"
)

func TestPageMetaModes(t *testing.T) {
	cursor := "next"
	total := 9
	offset := 2
	for _, tc := range []struct {
		name          string
		more          bool
		offset, total *int
	}{
		{"cursor", true, nil, nil}, {"offset", true, &offset, &total}, {"offset-last", false, &offset, nil}, {"cursor-last", false, nil, &total},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := pagination.NewPageMeta(2, 2, tc.more, &cursor, tc.total, tc.offset)
			raw, err := json.Marshal(meta)
			require.NoError(t, err)
			var out map[string]any
			require.NoError(t, json.Unmarshal(raw, &out))
			require.Equal(t, float64(2), out["returned"])
			require.Equal(t, float64(2), out["limit"])
			require.Equal(t, tc.more, out["has_more"])
			if tc.more {
				require.Equal(t, "next", out["next_cursor"])
			} else {
				require.Contains(t, out, "next_cursor")
				require.Nil(t, out["next_cursor"])
			}
			if tc.total == nil {
				require.NotContains(t, out, "total")
			} else {
				require.Equal(t, float64(9), out["total"])
			}
			if tc.offset == nil {
				require.NotContains(t, out, "offset")
				require.NotContains(t, out, "next_offset")
			} else {
				require.Equal(t, float64(2), out["offset"])
				require.Contains(t, out, "next_offset")
				if tc.more {
					require.Equal(t, float64(4), out["next_offset"])
				} else {
					require.Nil(t, out["next_offset"])
				}
			}
		})
	}
}
