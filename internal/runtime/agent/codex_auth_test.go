package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/stretchr/testify/require"
)

func TestPrepareCodexAuthSourceAndIsolation(t *testing.T) {
	home, explicit := t.TempDir(), t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(home, ".codex"), 0700))
	defaultAuth := []byte(`{"OPENAI_API_KEY":"default-test-secret"}`)
	explicitAuth := []byte(`{"tokens":{"access_token":"explicit-test-secret"}}`)
	require.NoError(t, os.WriteFile(filepath.Join(home, ".codex", "auth.json"), defaultAuth, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(explicit, "auth.json"), explicitAuth, 0600))
	for _, tc := range []struct {
		name string
		env  []string
		want []byte
	}{
		{"home fallback", []string{"HOME=" + home}, defaultAuth},
		{"explicit home wins", []string{"HOME=" + home, "CODEX_HOME=/missing", "CODEX_HOME=" + explicit}, explicitAuth},
	} {
		t.Run(tc.name, func(t *testing.T) {
			boot := t.TempDir()
			execution := &agentlaunch.PreparedExecution{Roots: agentlaunch.ExecutionRoots{BootRoot: boot}, Effects: []agentlaunch.RuntimeEffect{{ProviderEffect: "codex-auth-json"}}}
			require.NoError(t, prepareCodexAuth(context.Background(), tc.env, execution))
			got, err := os.ReadFile(filepath.Join(boot, "auth.json"))
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			st, err := os.Stat(filepath.Join(boot, "auth.json"))
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0600), st.Mode().Perm())
			// A provider refresh mutates only its private cache.
			require.NoError(t, os.WriteFile(filepath.Join(boot, "auth.json"), []byte(`{"refreshed":true}`), 0600))
			got, err = os.ReadFile(filepath.Join(explicit, "auth.json"))
			require.NoError(t, err)
			require.Equal(t, explicitAuth, got)
			got, err = os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
			require.NoError(t, err)
			require.Equal(t, defaultAuth, got)
		})
	}
}

func TestPrepareCodexAuthRejectsMissingOrMalformedCache(t *testing.T) {
	for _, content := range []string{"missing", "", "{}", "null", `{"secret":"do-not-log"`} {
		t.Run(content, func(t *testing.T) {
			source, boot := t.TempDir(), t.TempDir()
			if content != "missing" {
				require.NoError(t, os.WriteFile(filepath.Join(source, "auth.json"), []byte(content), 0600))
			}
			err := prepareCodexAuth(context.Background(), []string{"CODEX_HOME=" + source}, &agentlaunch.PreparedExecution{Roots: agentlaunch.ExecutionRoots{BootRoot: boot}})
			require.Error(t, err)
			require.NotContains(t, err.Error(), "do-not-log")
			require.Contains(t, err.Error(), "Codex login")
			_, statErr := os.Stat(filepath.Join(boot, "auth.json"))
			require.True(t, os.IsNotExist(statErr))
		})
	}
}
