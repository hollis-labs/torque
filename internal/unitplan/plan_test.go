package unitplan_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/unitplan"
)

func TestParseDefaults(t *testing.T) {
	p, err := unitplan.Parse([]byte(`{"workstream_id":"ws-1","goal":"ship it","done_when":"tests pass\n- docs updated"}`))
	require.NoError(t, err)
	assert.Equal(t, unitplan.Int(1), p.Workers)
	assert.Equal(t, "sol", p.WorkerRuntime)
	assert.Equal(t, "@chief", p.EscalateTo)
	assert.Equal(t, unitplan.MergeOnGreen, p.MergePolicy)
	assert.Equal(t, unitplan.RiskNormal, p.RiskTier)
	assert.Equal(t, unitplan.List{"tests pass", "docs updated"}, p.DoneWhen)
}

func TestParseTemplateStrings(t *testing.T) {
	p, err := unitplan.Parse([]byte(`{"workstream_id":"ws","goal":"g","done_when":["a"],"workers":"2","release":"true","scope":{"in":"pkg/a","out":["pkg/b"]}}`))
	require.NoError(t, err)
	assert.Equal(t, unitplan.Int(2), p.Workers)
	assert.True(t, bool(p.Release))
	assert.Equal(t, unitplan.List{"pkg/a"}, p.Scope.In)
}

func TestValidateRefusals(t *testing.T) {
	base := `"workstream_id":"ws","goal":"g","done_when":["a"]`
	cases := map[string]struct{ body, want string }{
		"no workstream": {`{"goal":"g","done_when":["a"]}`, "workstream_id"},
		"no goal":       {`{"workstream_id":"w","done_when":["a"]}`, "goal"},
		"no done_when":  {`{"workstream_id":"w","goal":"g"}`, "done_when"},
		"zero workers":  {`{` + base + `,"workers":-1}`, "workers"},
		"four workers":  {`{` + base + `,"workers":4}`, "3 workers"},
		"bad runtime":   {`{` + base + `,"worker_runtime":"gpt"}`, "worker_runtime"},
		"bad merge":     {`{` + base + `,"merge_policy":"yolo"}`, "merge_policy"},
		"bad risk":      {`{` + base + `,"risk_tier":"extreme"}`, "risk_tier"},
		"bad escalee":   {`{` + base + `,"escalate_to":"chief"}`, "@handle"},
		"timeout long":  {`{` + base + `,"timeout":"24h"}`, "12h"},
		"timeout junk":  {`{` + base + `,"timeout":"soon"}`, "timeout"},
		"unknown field": {`{` + base + `,"orchestrators":2}`, "orchestrators"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := unitplan.Parse([]byte(tc.body))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestFromMetadataMissing(t *testing.T) {
	_, err := unitplan.FromMetadata(map[string]any{})
	require.Error(t, err)
	assert.False(t, unitplan.Present(map[string]any{}))
}

func TestBriefCarriesTheContract(t *testing.T) {
	p, err := unitplan.Parse([]byte(`{"workstream_id":"ws-9","goal":"do X","done_when":["X works"],"scope":{"out":["billing"]},"evidence":["CI link"]}`))
	require.NoError(t, err)
	b := p.Brief("CW-1", "Title")
	for _, want := range []string{"CW-1", "ws-9", "do X", "X works", "billing", "CI link", "@chief", "RESULT:", "tether_team_report_result"} {
		assert.True(t, strings.Contains(b, want), "brief missing %q", want)
	}
}
