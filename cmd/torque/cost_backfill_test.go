package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// cost-backfill prices a legacy row from input and output tokens alone, which
// understates a Claude run (most of its usage is cache), so Claude's runtime is
// skipped under any of its spellings; the others are priced as before.
func TestBackfillSkipsProvider(t *testing.T) {
	for _, p := range []string{"claude", "claude-code", "claudecode", "Claude-Code", "anthropic"} {
		assert.True(t, backfillSkipsProvider(p), p)
	}
	for _, p := range []string{"codex", "openai", "opencode", "open-code", "copilot", "agy", ""} {
		assert.False(t, backfillSkipsProvider(p), p)
	}
}
