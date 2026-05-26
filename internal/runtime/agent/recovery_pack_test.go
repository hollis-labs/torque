package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShouldBuildRecoveryPack mirrors nanite's TestShouldBuildRecoveryPack:
// cold boot + prior history → recover; cold boot with no history (new session)
// → skip; live runtime (not cold) → no recovery.
func TestShouldBuildRecoveryPack(t *testing.T) {
	cases := []struct {
		name  string
		cold  bool
		prior int
		want  bool
	}{
		{"cold-with-history", true, 4, true},
		{"cold-no-history", true, 0, false},
		{"live-runtime", false, 4, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, ShouldBuildRecoveryPack(c.cold, c.prior))
		})
	}
}

// TestBuildRecoveryPack pins the rendered block contract: the framing
// instruction, RECOVERED CONTEXT marker, session identity, the replayed turns
// (oldest→newest), the pack-path pointer when set, and the closing tag.
func TestBuildRecoveryPack(t *testing.T) {
	pack := BuildRecoveryPack(RecoveryPackInput{
		Provider:       "claude",
		Role:           "orchestrator",
		Reason:         "orchestrator redispatch after prior session exited",
		PriorSessionID: "SES-PRIOR",
		History: []RecoveryTurn{
			{Role: "assistant", Text: "I started phase 1 and opened the PR."},
			{Role: "tool", Text: "called torque_task_transition"},
			{Role: "assistant", Text: "Waiting on the pr_review checkpoint."},
		},
		PackPath: "/boot/recovery.md",
	})

	for _, sub := range []string{
		"<recovered-session-context>",
		"RECOVERED CONTEXT",
		"orchestrator",
		"SES-PRIOR",
		"orchestrator redispatch after prior session exited",
		"I started phase 1 and opened the PR.",
		"called torque_task_transition",
		"Waiting on the pr_review checkpoint.",
		"/boot/recovery.md",
		"</recovered-session-context>",
	} {
		assert.Contains(t, pack, sub, "pack missing %q", sub)
	}

	// Ordering: oldest turn precedes newest turn in the rendered block.
	assert.Less(t,
		strings.Index(pack, "I started phase 1"),
		strings.Index(pack, "Waiting on the pr_review"),
		"turns must render oldest → newest",
	)
}

// TestBuildRecoveryPack_NoPackPathOmitsPointer confirms the "full pack at ..."
// line is omitted when PackPath is empty (the Redispatch path builds the block
// before the new bootDir is known), mirroring nanite's nil-PackPath handling.
func TestBuildRecoveryPack_NoPackPathOmitsPointer(t *testing.T) {
	pack := BuildRecoveryPack(RecoveryPackInput{
		History: []RecoveryTurn{{Role: "assistant", Text: "did a thing"}},
	})
	assert.NotContains(t, pack, "The full pack is also at")
	assert.Contains(t, pack, "did a thing")
}

// TestBuildRecoveryPack_Truncation bounds each replayed turn at
// recoveryTurnMaxChars and appends the truncation marker.
func TestBuildRecoveryPack_Truncation(t *testing.T) {
	long := strings.Repeat("x", recoveryTurnMaxChars+500)
	pack := BuildRecoveryPack(RecoveryPackInput{
		History: []RecoveryTurn{{Role: "assistant", Text: long}},
	})
	assert.Contains(t, pack, "…[truncated]")
	// The full over-length payload must not appear verbatim.
	assert.NotContains(t, pack, long)
	// The kept prefix is exactly recoveryTurnMaxChars chars of x's.
	assert.Contains(t, pack, strings.Repeat("x", recoveryTurnMaxChars)+" …[truncated]")
}

// writeStream writes JSONL lines to <dir>/logs/stream.jsonl and returns the
// path, materializing the logs dir the way the stream sidecar does.
func writeStream(t *testing.T, dir string, lines ...string) string {
	t.Helper()
	logDir := filepath.Join(dir, "logs")
	require.NoError(t, os.MkdirAll(logDir, 0o700))
	p := filepath.Join(logDir, "stream.jsonl")
	require.NoError(t, os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	return p
}

// TestReconstructTurnsFromStream_Coalesce verifies that consecutive delta
// events coalesce into one assistant turn, a tool_use flushes the in-progress
// assistant turn and emits a tool turn, and non-content events are skipped.
func TestReconstructTurnsFromStream_Coalesce(t *testing.T) {
	dir := t.TempDir()
	p := writeStream(t, dir,
		`{"ts":"2026-05-25T10:00:00Z","type":"delta","content":"Hello "}`,
		`{"ts":"2026-05-25T10:00:01Z","type":"delta","content":"world"}`,
		`{"ts":"2026-05-25T10:00:02Z","type":"tool_use","tool_use":{"name":"torque_task_get"}}`,
		`{"ts":"2026-05-25T10:00:03Z","type":"usage","usage":{"input_tokens":10}}`,
		`{"ts":"2026-05-25T10:00:04Z","type":"delta","content":"done now"}`,
		`{"ts":"2026-05-25T10:00:05Z","type":"done"}`,
	)

	turns, err := ReconstructTurnsFromStream(p, recoveryHistoryTurns)
	require.NoError(t, err)
	require.Len(t, turns, 3)

	assert.Equal(t, "assistant", turns[0].Role)
	assert.Equal(t, "Hello world", turns[0].Text)

	assert.Equal(t, "tool", turns[1].Role)
	assert.Contains(t, turns[1].Text, "torque_task_get")

	assert.Equal(t, "assistant", turns[2].Role)
	assert.Equal(t, "done now", turns[2].Text)
}

// TestReconstructTurnsFromStream_TrailingTrim keeps only the trailing maxTurns
// (chronological order preserved), mirroring nanite's history[len-N:] trim.
func TestReconstructTurnsFromStream_TrailingTrim(t *testing.T) {
	dir := t.TempDir()
	// 5 distinct assistant turns, each separated by a tool_use so they don't
	// coalesce. We then trim to the last 2.
	lines := []string{
		`{"type":"delta","content":"turn1"}`,
		`{"type":"tool_use","tool_use":{"name":"t1"}}`,
		`{"type":"delta","content":"turn2"}`,
		`{"type":"tool_use","tool_use":{"name":"t2"}}`,
		`{"type":"delta","content":"turn3"}`,
	}
	p := writeStream(t, dir, lines...)

	turns, err := ReconstructTurnsFromStream(p, 2)
	require.NoError(t, err)
	require.Len(t, turns, 2)
	// Trailing two of [turn1, t1, turn2, t2, turn3] are [t2, turn3].
	assert.Equal(t, "tool", turns[0].Role)
	assert.Contains(t, turns[0].Text, "t2")
	assert.Equal(t, "turn3", turns[1].Text)
}

// TestReconstructTurnsFromStream_LongStreamKeepsNewest guards the trailing-window
// semantics on a long stream: a session with far more turns than the window must
// recover its MOST RECENT turns, not its opening ones. (Regression test for a
// head-capped scan that returned the oldest turns for long-lived sessions —
// exactly the recovery target.)
func TestReconstructTurnsFromStream_LongStreamKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	// 500 distinct assistant turns, each separated by a tool_use so they don't
	// coalesce. Window of 3 must yield the last three (turn498..turn499 around
	// the trailing tool calls), never turn0/turn1.
	const total = 500
	lines := make([]string, 0, total*2)
	for i := 0; i < total; i++ {
		lines = append(lines, fmt.Sprintf(`{"type":"delta","content":"turn%d"}`, i))
		lines = append(lines, fmt.Sprintf(`{"type":"tool_use","tool_use":{"name":"t%d"}}`, i))
	}
	p := writeStream(t, dir, lines...)

	turns, err := ReconstructTurnsFromStream(p, 3)
	require.NoError(t, err)
	require.Len(t, turns, 3)
	// Trailing three of [..., turn499, t499] are [t498, turn499, t499].
	assert.Contains(t, turns[0].Text, "t498")
	assert.Equal(t, "turn499", turns[1].Text)
	assert.Contains(t, turns[2].Text, "t499")
	// The opening turns must NOT appear in the recovered window.
	for _, tn := range turns {
		assert.NotContains(t, tn.Text, "turn0")
		assert.NotContains(t, tn.Text, "turn1")
	}
}

// TestReconstructTurnsFromStream_OversizedLineDoesNotPoisonScan guards the
// bufio.Reader switch: a single oversized stream line (e.g. a tool_use with a
// multi-MiB payload) must NOT stop reconstruction of subsequent (more recent)
// turns. bufio.Scanner's fixed max-token limit would silently drop everything
// past the first overlong line — exactly the trailing turns the recovery
// targets. The Reader.ReadString loop has no fixed line cap.
func TestReconstructTurnsFromStream_OversizedLineDoesNotPoisonScan(t *testing.T) {
	dir := t.TempDir()
	// 5 MiB padding — comfortably exceeds the prior Scanner buffer cap.
	huge := strings.Repeat("a", 5*1024*1024)
	lines := []string{
		`{"type":"delta","content":"turn0"}`,
		fmt.Sprintf(`{"type":"tool_use","tool_use":{"name":"BigTool"},"payload":"%s"}`, huge),
		`{"type":"delta","content":"turn-final"}`,
	}
	p := writeStream(t, dir, lines...)

	turns, err := ReconstructTurnsFromStream(p, 10)
	require.NoError(t, err)
	require.Len(t, turns, 3, "all three turns must reconstruct past the oversized middle line")
	assert.Equal(t, "turn0", turns[0].Text)
	assert.Contains(t, turns[1].Text, "BigTool")
	assert.Equal(t, "turn-final", turns[2].Text,
		"the turn AFTER the oversized line must appear (regression: bufio.Scanner would have dropped it)")
}

// TestReconstructTurnsFromStream_MissingFile is best-effort: a missing stream
// file returns (nil, nil) so a brand-new session falls through to a normal
// cold boot.
func TestReconstructTurnsFromStream_MissingFile(t *testing.T) {
	turns, err := ReconstructTurnsFromStream(filepath.Join(t.TempDir(), "nope.jsonl"), 20)
	require.NoError(t, err)
	assert.Nil(t, turns)
}

// TestReconstructTurnsFromStream_MalformedSkipped skips unparseable lines
// individually rather than failing the whole reconstruction.
func TestReconstructTurnsFromStream_MalformedSkipped(t *testing.T) {
	dir := t.TempDir()
	p := writeStream(t, dir,
		`{"type":"delta","content":"good "}`,
		`{not valid json`,
		`{"type":"delta","content":"text"}`,
	)
	turns, err := ReconstructTurnsFromStream(p, 20)
	require.NoError(t, err)
	require.Len(t, turns, 1)
	assert.Equal(t, "good text", turns[0].Text)
}

// TestBuildRecoveryPackForPriorSession_ColdBootWithHistory is the integration
// test for the high-level helper: a terminal prior session with a stream trace
// yields a recovery pack; a session with no recorded workspace (or empty trace)
// yields "".
func TestBuildRecoveryPackForPriorSession_ColdBootWithHistory(t *testing.T) {
	ws := t.TempDir()
	writeStream(t, ws,
		`{"type":"delta","content":"opened PR #42 for phase 1"}`,
		`{"type":"tool_use","tool_use":{"name":"torque_task_transition"}}`,
	)
	meta, err := encodeMeta(map[string]string{
		metaKeyWorkspaceDir: ws,
		"role":              "orchestrator",
	})
	require.NoError(t, err)

	prior := &sqlstore.SessionRecord{
		ID:       "SES-PRIOR",
		Provider: "claude",
		State:    "crashed",
		MetaJSON: meta,
	}

	pack, turns, err := BuildRecoveryPackForPriorSession(prior, "redispatch test")
	require.NoError(t, err)
	assert.Equal(t, 2, turns)
	assert.Contains(t, pack, "<recovered-session-context>")
	assert.Contains(t, pack, "opened PR #42 for phase 1")
	assert.Contains(t, pack, "torque_task_transition")
	assert.Contains(t, pack, "SES-PRIOR")
	assert.Contains(t, pack, "orchestrator")
	// The high-level helper now threads PackPath=recoveryPackFileName so the
	// inlined block points the agent at the on-disk copy in its cwd (the boot
	// dir post-Boot). Use a literal here, not the unexported const.
	assert.Contains(t, pack, "recovery.md")
}

func TestBuildRecoveryPackForPriorSession_NoWorkspace(t *testing.T) {
	prior := &sqlstore.SessionRecord{ID: "SES-PRIOR", State: "crashed", MetaJSON: "{}"}
	pack, turns, err := BuildRecoveryPackForPriorSession(prior, "redispatch test")
	require.NoError(t, err)
	assert.Equal(t, 0, turns)
	assert.Empty(t, pack)
}

func TestBuildRecoveryPackForPriorSession_NilPrior(t *testing.T) {
	pack, turns, err := BuildRecoveryPackForPriorSession(nil, "redispatch test")
	require.NoError(t, err)
	assert.Equal(t, 0, turns)
	assert.Empty(t, pack)
}

// TestWriteRecoveryPackFile writes the pointer file and no-ops on empty bootDir.
func TestWriteRecoveryPackFile(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteRecoveryPackFile(dir, "PACK BODY")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, recoveryPackFileName), path)
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "PACK BODY\n", string(b))

	// Empty bootDir → no-op.
	path, err = WriteRecoveryPackFile("", "x")
	require.NoError(t, err)
	assert.Empty(t, path)
}
