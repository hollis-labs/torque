package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CW-20261001-0121 item 3: profile args go among the CLI's options, so a
// literal `--` or a leading positional is rejected at load and in lint.
func TestValidateProfileArgs(t *testing.T) {
	for _, ok := range [][]string{
		nil,
		{"--effort", "high"},
		{"--dangerously-skip-permissions"},
		{"-c", "model=x", "--max-turns", "7"},
		{"--add-dir=/srv/data"},
	} {
		assert.NoError(t, validateProfileArgs("w", ok), "%q", ok)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--"}, `agent_profiles["w"].args[0]: "--" is not allowed`},
		{[]string{"--max-turns", "7", "--", "x"}, `agent_profiles["w"].args[2]: "--" is not allowed`},
		{[]string{"high"}, `agent_profiles["w"].args[0]: "high" is not an option`},
		{[]string{"", "--x"}, `agent_profiles["w"].args[0]: "" is not an option`},
	} {
		err := validateProfileArgs("w", tc.args)
		require.Error(t, err, "%q", tc.args)
		assert.Contains(t, err.Error(), tc.want)
	}
}

func TestLoadProfilesFileRejectsBadArgs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`agent_profiles:
  worker:
    executor: cli
    provider: claude-code
    args: ["--model", "x", "--", "then text"]
`), 0o644))
	_, err := LoadProfilesFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), path)
	assert.Contains(t, err.Error(), `agent_profiles["worker"].args[2]: "--" is not allowed`)
}

func TestLintProfilesYAMLReportsBadArgs(t *testing.T) {
	problems, err := LintProfilesYAML([]byte(`agent_profiles:
  worker:
    executor: cli
    provider: claude-code
    args: ["positional", "--x"]
`))
	require.NoError(t, err)
	var found bool
	for _, p := range problems {
		if p.Path == "agent_profiles.worker.args" {
			found = true
			assert.Contains(t, p.Message, `"positional" is not an option`)
		}
	}
	assert.True(t, found, "lint must report the args problem: %v", problems)
}
