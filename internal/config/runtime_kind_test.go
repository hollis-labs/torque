package config_test

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
)

// TestLoadProfilesMapsLegacyRuntimeKind is CW-20261001-0063: a profiles.yaml
// written before the runtimes.Mode spellings keeps loading. Each retired
// runtime_kind becomes its current mode with one warning naming the
// profile; current values load silently, and unknown values are left for
// boot-time validation as before.
func TestLoadProfilesMapsLegacyRuntimeKind(t *testing.T) {
	yaml := `agent_profiles:
  oc-sub:
    executor: cli
    provider: opencode
    runtime_kind: subprocess
  oc-serve:
    executor: cli
    provider: opencode
    runtime_kind: serve-http
  old-cli:
    executor: cli
    provider: codex
    runtime_kind: cli
  codex-app:
    executor: cli
    provider: codex
    runtime_kind: app-server
  claude-dbg:
    executor: cli
    provider: claude-code
    runtime_kind: pty-debug
  claude:
    executor: cli
    provider: claude-code
    runtime_kind: streaming-stdio
  default-kind:
    executor: cli
    provider: claude-code
  typo:
    executor: cli
    provider: codex
    runtime_kind: warp-drive
`
	path := filepath.Join(t.TempDir(), "profiles.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o644))

	var logs bytes.Buffer
	prev, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prev); log.SetFlags(prevFlags) })

	pf, err := config.LoadProfilesFile(path)
	require.NoError(t, err)

	want := map[string]string{
		"oc-sub":       "subprocess-per-turn",
		"oc-serve":     "http-sse",
		"old-cli":      "subprocess-per-turn",
		"codex-app":    "jsonrpc-stdio",
		"claude-dbg":   "pty",
		"claude":       "streaming-stdio",
		"default-kind": "",
		"typo":         "warp-drive",
	}
	for name, kind := range want {
		assert.Equal(t, kind, pf.Profiles[name].RuntimeKind, name)
	}

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	assert.Len(t, lines, 5, "one warning per profile with a retired runtime_kind:\n%s", logs.String())
	for _, name := range []string{"oc-sub", "oc-serve", "old-cli", "codex-app", "claude-dbg"} {
		assert.Contains(t, logs.String(), `agent_profiles["`+name+`"] runtime_kind`)
	}
	assert.Contains(t, logs.String(), "debug posture has no Torque equivalent and is dropped",
		"pty-debug's warning says the debug posture is dropped")
	assert.NotContains(t, logs.String(), `"claude"]`)
	assert.NotContains(t, logs.String(), `"typo"]`)
}
