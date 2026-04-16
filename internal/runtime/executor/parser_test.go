package executor_test

import (
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLineDone(t *testing.T) {
	sig := executor.ParseLine("CLOCKWORK_DONE")
	assert.Equal(t, executor.SignalDone, sig.Type)
	assert.Equal(t, "", sig.Payload)
}

func TestParseLineDoneTrimmed(t *testing.T) {
	sig := executor.ParseLine("  CLOCKWORK_DONE  ")
	assert.Equal(t, executor.SignalDone, sig.Type)
}

func TestParseLineReview(t *testing.T) {
	sig := executor.ParseLine("CLOCKWORK_REVIEW")
	assert.Equal(t, executor.SignalReview, sig.Type)
}

func TestParseLineBlocked(t *testing.T) {
	sig := executor.ParseLine("CLOCKWORK_BLOCKED: waiting for API key")
	assert.Equal(t, executor.SignalBlocked, sig.Type)
	assert.Equal(t, "waiting for API key", sig.Payload)
}

func TestParseLineBlockedEmpty(t *testing.T) {
	sig := executor.ParseLine("CLOCKWORK_BLOCKED:")
	assert.Equal(t, executor.SignalBlocked, sig.Type)
	assert.Equal(t, "", sig.Payload)
}

func TestParseLineNote(t *testing.T) {
	sig := executor.ParseLine("CLOCKWORK_NOTE: refactored auth module")
	assert.Equal(t, executor.SignalNote, sig.Type)
	assert.Equal(t, "refactored auth module", sig.Payload)
}

func TestParseLineNewTask(t *testing.T) {
	sig := executor.ParseLine("CLOCKWORK_TASK: Add unit tests for login handler")
	assert.Equal(t, executor.SignalNewTask, sig.Type)
	assert.Equal(t, "Add unit tests for login handler", sig.Payload)
}

func TestParseLineTokens(t *testing.T) {
	sig := executor.ParseLine("CLOCKWORK_TOKENS: prompt=1500 completion=800 cost=0.04")
	assert.Equal(t, executor.SignalTokens, sig.Type)
	assert.Equal(t, "prompt=1500 completion=800 cost=0.04", sig.Payload)
}

func TestParseLineLogLine(t *testing.T) {
	sig := executor.ParseLine("Running tests... OK")
	assert.Equal(t, executor.SignalLogLine, sig.Type)
	assert.Equal(t, "Running tests... OK", sig.Payload)
}

func TestParseLineEmptyLine(t *testing.T) {
	sig := executor.ParseLine("")
	assert.Equal(t, executor.SignalLogLine, sig.Type)
}

func TestParseLineJSONCheckpoint(t *testing.T) {
	line := `{"signal": "CLOCKWORK_CHECKPOINT", "label": "tests_passing", "data": {"tests": 42}}`
	sig := executor.ParseLine(line)
	assert.Equal(t, executor.SignalCheckpoint, sig.Type)
	assert.Equal(t, line, sig.Payload)
}

func TestParseLineJSONProgress(t *testing.T) {
	line := `{"signal": "CLOCKWORK_PROGRESS", "progress": 0.75, "message": "3 of 4 files done"}`
	sig := executor.ParseLine(line)
	assert.Equal(t, executor.SignalProgress, sig.Type)
	assert.Equal(t, line, sig.Payload)
}

func TestParseLineJSONSubtask(t *testing.T) {
	line := `{"signal": "CLOCKWORK_SUBTASK", "title": "Fix auth handler", "status": "done"}`
	sig := executor.ParseLine(line)
	assert.Equal(t, executor.SignalSubtask, sig.Type)
}

func TestParseLineJSONArtifact(t *testing.T) {
	line := `{"signal": "CLOCKWORK_ARTIFACT", "type": "diff", "content": "--- a/file.go\n+++ b/file.go"}`
	sig := executor.ParseLine(line)
	assert.Equal(t, executor.SignalArtifact, sig.Type)
	assert.Equal(t, line, sig.Payload)
}

func TestParseLineJSONDone(t *testing.T) {
	line := `{"signal": "CLOCKWORK_DONE", "summary": "All tasks complete"}`
	sig := executor.ParseLine(line)
	assert.Equal(t, executor.SignalDone, sig.Type)
}

func TestParseLineJSONUnknownSignal(t *testing.T) {
	line := `{"signal": "CLOCKWORK_FUTURE_SIGNAL", "data": "something"}`
	sig := executor.ParseLine(line)
	// Unknown signals fall back to SignalNote for forward compatibility
	assert.Equal(t, executor.SignalNote, sig.Type)
	assert.Equal(t, line, sig.Payload)
}

func TestParseLineJSONNoSignalField(t *testing.T) {
	line := `{"key": "value", "foo": "bar"}`
	sig := executor.ParseLine(line)
	// JSON without "signal" field is just a log line
	assert.Equal(t, executor.SignalLogLine, sig.Type)
}

func TestParseLineJSONMalformed(t *testing.T) {
	line := `{this is not valid json`
	sig := executor.ParseLine(line)
	assert.Equal(t, executor.SignalLogLine, sig.Type)
}

func TestParseLine_CheckpointInline(t *testing.T) {
	// CLOCKWORK_CHECKPOINT <correlation_id> <type> <base64(payload_json)>
	sig := executor.ParseLine("CLOCKWORK_CHECKPOINT 01H-CORR collect_data eyJxIjoicGljayJ9")
	assert.Equal(t, executor.SignalCheckpoint, sig.Type)
	assert.Equal(t, "01H-CORR collect_data eyJxIjoicGljayJ9", sig.Payload)
}

func TestParseLine_CheckpointAwaitInline(t *testing.T) {
	sig := executor.ParseLine("CLOCKWORK_CHECKPOINT_AWAIT 01H-CORR")
	assert.Equal(t, executor.SignalCheckpointAwait, sig.Type)
	assert.Equal(t, "01H-CORR", sig.Payload)
}

func TestParseCheckpointPayload(t *testing.T) {
	t.Run("decodes three-part payload", func(t *testing.T) {
		// payload_json is `{"q":"pick"}` base64-encoded as `eyJxIjoicGljayJ9`.
		corr, typ, pj, err := executor.ParseCheckpointPayload("01H-CORR collect_data eyJxIjoicGljayJ9")
		require.NoError(t, err)
		assert.Equal(t, "01H-CORR", corr)
		assert.Equal(t, "collect_data", typ)
		assert.Equal(t, `{"q":"pick"}`, pj)
	})

	t.Run("rejects malformed payload", func(t *testing.T) {
		_, _, _, err := executor.ParseCheckpointPayload("only-two parts")
		require.Error(t, err)
	})

	t.Run("rejects bad base64", func(t *testing.T) {
		_, _, _, err := executor.ParseCheckpointPayload("corr type not-base64!")
		require.Error(t, err)
	})
}

func TestParseArtifactPayload(t *testing.T) {
	t.Run("full payload", func(t *testing.T) {
		payload := `{"signal":"CLOCKWORK_ARTIFACT","type":"diff","content":"--- a/x\n+++ b/x","url":"https://example.com/x","file_path":"x.go","metadata":{"lines":42}}`
		art, err := executor.ParseArtifactPayload(payload)
		require.NoError(t, err)
		assert.Equal(t, "diff", art.Type)
		assert.Equal(t, "--- a/x\n+++ b/x", art.Content)
		assert.Equal(t, "https://example.com/x", art.URL)
		assert.Equal(t, "x.go", art.FilePath)
		require.NotNil(t, art.Metadata)
		// JSON numbers decode to float64
		assert.Equal(t, float64(42), art.Metadata["lines"])
	})

	t.Run("minimal payload", func(t *testing.T) {
		payload := `{"signal":"CLOCKWORK_ARTIFACT","type":"log"}`
		art, err := executor.ParseArtifactPayload(payload)
		require.NoError(t, err)
		assert.Equal(t, "log", art.Type)
		assert.Equal(t, "", art.Content)
		assert.Equal(t, "", art.URL)
		assert.Equal(t, "", art.FilePath)
		assert.Nil(t, art.Metadata)
	})

	t.Run("missing type", func(t *testing.T) {
		payload := `{"signal":"CLOCKWORK_ARTIFACT","content":"body"}`
		_, err := executor.ParseArtifactPayload(payload)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "type")
	})

	t.Run("invalid json", func(t *testing.T) {
		_, err := executor.ParseArtifactPayload("not json")
		require.Error(t, err)
	})

	t.Run("metadata preserved", func(t *testing.T) {
		payload := `{"signal":"CLOCKWORK_ARTIFACT","type":"diff","metadata":{"lines":42}}`
		art, err := executor.ParseArtifactPayload(payload)
		require.NoError(t, err)
		require.NotNil(t, art.Metadata)
		assert.Equal(t, float64(42), art.Metadata["lines"])
	})
}

func TestParseTokenPayload(t *testing.T) {
	prompt, completion, cost := executor.ParseTokenPayload("prompt=1500 completion=800 cost=0.04")
	assert.Equal(t, int64(1500), prompt)
	assert.Equal(t, int64(800), completion)
	assert.InDelta(t, 0.04, cost, 0.001)
}

func TestParseTokenPayloadPartial(t *testing.T) {
	prompt, completion, cost := executor.ParseTokenPayload("prompt=500")
	assert.Equal(t, int64(500), prompt)
	assert.Equal(t, int64(0), completion)
	assert.Equal(t, float64(0), cost)
}

func TestParseTokenPayloadEmpty(t *testing.T) {
	prompt, completion, cost := executor.ParseTokenPayload("")
	assert.Equal(t, int64(0), prompt)
	assert.Equal(t, int64(0), completion)
	assert.Equal(t, float64(0), cost)
}
