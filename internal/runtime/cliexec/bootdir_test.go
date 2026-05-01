package cliexec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBootDir_PlantsExpectedFiles(t *testing.T) {
	bootDir, err := setupBootDir("CW-20260427-0059", 42, "agent persona\n\nbe helpful", "/work/clockwork-manifold", 53420)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })

	require.True(t, strings.HasPrefix(filepath.Base(bootDir), "clockwork-cli-boot-CW-20260427-0059-r42-"),
		"boot dir name should encode task + run for inspection: %s", bootDir)

	claudeMD, err := os.ReadFile(filepath.Join(bootDir, "CLAUDE.md"))
	require.NoError(t, err)
	body := string(claudeMD)
	assert.Contains(t, body, "agent persona", "system prompt should be at the top")
	assert.Contains(t, body, "be helpful", "system prompt body preserved")
	assert.Contains(t, body, "/work/clockwork-manifold", "project root pointer present")
	assert.Contains(t, body, "clockwork_loopback", "loopback server name surfaced")
	assert.Contains(t, body, "clockwork_task_summary", "interpretive tools listed")
	assert.Contains(t, body, "After context compaction", "reload directive present")
	assert.Contains(t, body, bootDir, "reload directive points at this boot dir")

	mcpRaw, err := os.ReadFile(filepath.Join(bootDir, ".mcp.json"))
	require.NoError(t, err)

	var mcp struct {
		MCPServers map[string]struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(mcpRaw, &mcp))
	require.Contains(t, mcp.MCPServers, "clockwork_loopback")
	loopback := mcp.MCPServers["clockwork_loopback"]
	assert.Equal(t, "http", loopback.Type)
	assert.Equal(t, "http://127.0.0.1:53420/mcp", loopback.URL)
}

func TestBootDir_EmptySystemPromptOmitsPreamble(t *testing.T) {
	bootDir, err := setupBootDir("CW-X", 1, "", "/p", 1234)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })

	claudeMD, err := os.ReadFile(filepath.Join(bootDir, "CLAUDE.md"))
	require.NoError(t, err)
	body := string(claudeMD)
	assert.False(t, strings.HasPrefix(body, "\n---\n"), "no leading separator when system prompt is empty")
	assert.True(t, strings.HasPrefix(body, "**Project root:**"), "project root section is the leading content: %q", body[:50])
}

func TestBootDir_CleanedUpOnExit(t *testing.T) {
	bootDir, err := setupBootDir("CW-X", 1, "sp", "/p", 1234)
	require.NoError(t, err)

	_, err = os.Stat(bootDir)
	require.NoError(t, err, "boot dir exists after setup")

	require.NoError(t, os.RemoveAll(bootDir))

	_, err = os.Stat(bootDir)
	assert.True(t, os.IsNotExist(err), "boot dir is gone after RemoveAll")
}

func TestSanitizeTaskID(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"CW-20260427-0059", "CW-20260427-0059"},
		{"task/with/slash", "task_with_slash"},
		{"task with spaces", "task_with_spaces"},
		{"weird*chars$here", "weird_chars_here"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			assert.Equal(t, c.want, sanitizeTaskID(c.in))
		})
	}
}
