package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/go-providers/provider"
)

// Prepare credentials after pure materialization, before CODEX_HOME is
// redirected to the boot directory. Keep secret bytes out of launch plans,
// artifact trees, diagnostics and task metadata. The normal boot-directory
// teardown owns the resulting private copy, including any token refreshes.
func prepareCodexAuth(ctx context.Context, env []string, execution *agentlaunch.PreparedExecution) error {
	values := make(map[string]string)
	for _, entry := range env {
		if key, value, ok := strings.Cut(entry, "="); ok {
			values[key] = value
		}
	}
	authHome := values["CODEX_HOME"]
	if authHome == "" {
		home := values["HOME"]
		if home == "" {
			var err error
			home, err = os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("resolve Codex login home: %w", err)
			}
		}
		authHome = filepath.Join(home, ".codex")
	}
	authPath := filepath.Join(authHome, "auth.json")
	data, err := os.ReadFile(authPath)
	if err != nil {
		return fmt.Errorf("read Codex login cache at %s: %w; configure a file-backed Codex login for the Torque daemon", authPath, err)
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || len(object) == 0 {
		return fmt.Errorf("Codex login cache at %s is empty or invalid; refresh the daemon's file-backed Codex login", authPath)
	}
	projection := provider.ProviderProjection{Provider: provider.ProviderCodex}
	for _, effect := range execution.Effects {
		if effect.ProviderEffect == string(provider.EffectCodexAuthJSON) {
			projection.Effects = append(projection.Effects, provider.ProviderEffect{
				Kind: provider.EffectCodexAuthJSON, Destination: "auth.json",
			})
		}
	}
	_, err = provider.PrepareRuntime(ctx, provider.RuntimePreparationRequest{
		Projection:      projection,
		Roots:           provider.ProjectionRoots{BootRoot: execution.Roots.BootRoot},
		Policy:          provider.PreparationPolicy{AllowCredentials: true},
		RequiredEffects: []provider.ProviderEffectKind{provider.EffectCodexAuthJSON},
		CredentialResolver: provider.CredentialResolverFunc(func(context.Context, provider.CredentialRequest) (provider.Credential, error) {
			return provider.Credential{Bytes: data, Mode: 0600}, nil
		}),
	})
	return err
}
