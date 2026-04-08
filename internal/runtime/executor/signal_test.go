package executor_test

import (
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
)

func TestSignalTypeStrings(t *testing.T) {
	tests := []struct {
		signal executor.SignalType
		str    string
	}{
		{executor.SignalLogLine, "log_line"},
		{executor.SignalDone, "CLOCKWORK_DONE"},
		{executor.SignalBlocked, "CLOCKWORK_BLOCKED"},
		{executor.SignalReview, "CLOCKWORK_REVIEW"},
		{executor.SignalNote, "CLOCKWORK_NOTE"},
		{executor.SignalNewTask, "CLOCKWORK_TASK"},
		{executor.SignalTokens, "CLOCKWORK_TOKENS"},
		{executor.SignalCheckpoint, "CLOCKWORK_CHECKPOINT"},
		{executor.SignalProgress, "CLOCKWORK_PROGRESS"},
		{executor.SignalSubtask, "CLOCKWORK_SUBTASK"},
		{executor.SignalArtifact, "CLOCKWORK_ARTIFACT"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.str, tt.signal.String(), "SignalType.String() for %d", tt.signal)
	}
}

func TestSignalTypeFromString(t *testing.T) {
	sig, ok := executor.SignalTypeFromString("CLOCKWORK_DONE")
	assert.True(t, ok)
	assert.Equal(t, executor.SignalDone, sig)

	sig, ok = executor.SignalTypeFromString("CLOCKWORK_ARTIFACT")
	assert.True(t, ok)
	assert.Equal(t, executor.SignalArtifact, sig)

	_, ok = executor.SignalTypeFromString("INVALID_SIGNAL")
	assert.False(t, ok)
}

func TestEventTypeStrings(t *testing.T) {
	assert.Equal(t, "log", executor.EventLog.String())
	assert.Equal(t, "signal", executor.EventSignal.String())
	assert.Equal(t, "artifact", executor.EventArtifact.String())
	assert.Equal(t, "progress", executor.EventProgress.String())
	assert.Equal(t, "token_usage", executor.EventTokenUsage.String())
}
