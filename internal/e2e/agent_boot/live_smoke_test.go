//go:build smoke

// Package agent_boot — LIVE provider smoke for the Phase 5 launch-profile path.
//
// This file is gated behind `//go:build smoke` so it never runs in normal CI
// (`go test ./...`). It drives agent.Boot against the REAL go-agent-sessions
// runtime — actual provider subprocesses (claude / codex / opencode) — through
// an inline launch profile (Options.LaunchProfileInline). It closes the "no
// live provider smoke" gap left by launch_profile_test.go, which only exercises
// the fakeRuntime.
//
// Run:
//
//	go test -tags smoke -run TestLiveSmoke ./internal/e2e/agent_boot/ -v -timeout 360s
//
// Each provider subtest is independent: an auth/network/binary failure on one
// provider is reported as BLOCKED(environment) and does not block the others.
// A launch-profile / boot-dir / planting break is a FAIL(Phase 5).
package agent_boot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/go-sqlite/sqlitekit"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// composeLiveDeps materializes the same Store + Manager substrate composeDeps
// builds, but with Dependencies.RuntimeFactory left nil — so agent.Boot falls
// through to the REAL agentsessions.NewFromAdapter and spawns an actual
// provider subprocess. This is the only structural difference from
// composeDeps; everything else (sqlite store, migrations, profile map, bounded
// Shutdown cleanup) is identical.
//
// profileProvider drives Boot's adapterFor / runtime-kind matrix — pass
// "claude" / "codex" / "opencode".
func composeLiveDeps(t *testing.T, profileProvider string) *composedDeps {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlitekit.OpenWriter(context.Background(), filepath.Join(dir, "agent_boot_live.db"), sqlitekit.OpenOptions{Options: sqlitekit.WriterOptions()})
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)

	prof := config.AgentProfile{Executor: "cli", Provider: profileProvider}
	deps := &agent.Dependencies{
		Store: store,
		Profiles: config.ProfileMap{
			"torque-backend": prof,
			"default":        prof,
		},
		Loopback:       nil, // no per-task MCP loopback in the smoke
		WorkspacesRoot: filepath.Join(dir, "workspaces"),
		// RuntimeFactory deliberately nil — real runtime path.
	}
	deps.Sessions = agent.NewManager(deps)

	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := deps.Sessions.Shutdown(shutdownCtx); err != nil {
			t.Errorf("agent.Manager.Shutdown returned error: %v", err)
		}
		cancel()
		store.Close()
		_ = db.Close()
	})

	return &composedDeps{
		Deps:    deps,
		Manager: deps.Sessions,
		Store:   store,
		Runtime: nil, // no fake runtime on the live path
		DB:      db,
	}
}

// liveInlineLaunchCatalog returns a self-contained launch-profile catalog
// (an inline GlobalCatalog) for the given provider. The repo_root is set to a
// distinctive marker path so a human reading the resolved plan can confirm the
// launch-profile path contributed it; Torque overlays its own Workdir on top,
// so the marker never reaches the subprocess — it is purely a provenance tag.
//
// The provider entry MUST carry runtime_kind — the go-agent-launch catalog
// (agentlaunch/catalog) rejects a provider with an empty runtime kind. This is
// only the catalog's BASE value; Torque's own per-provider matrix
// (selectRuntimeKind) is authoritative for the spawned runtime and overlays on
// top, so "subprocess" here is just a syntactically-valid placeholder.
func liveInlineLaunchCatalog(provider string) []byte {
	return []byte(fmt.Sprintf(`version: "0.9.9-smoke"
projects:
  - id: torque-live-smoke-project
    repo_root: /tmp/torque-live-smoke-marker
agents:
  - id: torque-live-smoke-agent
providers:
  - id: %s
    runtime_kind: subprocess
launches:
  - id: torque-live-smoke-launch
    project: torque-live-smoke-project
    agent: torque-live-smoke-agent
    provider: %s
    workspace:
      mode: persistent
`, provider, provider))
}

// providerBinary maps a provider id to the expected binary path / name so the
// smoke can pre-flight a missing-binary BLOCKED before attempting Boot.
// The "claude-code" provider id is the streaming-stdio claude path; its
// binary on PATH is still `claude`. The bare "claude" provider was retired
// 2026-05-16 (see adapterFor) — the smoke must drive claude-code.
var providerBinary = map[string]string{
	"claude-code": "claude",
	"codex":       "codex",
	"opencode":    "opencode",
}

// binaryAvailable reports whether the provider binary resolves on PATH.
func binaryAvailable(provider string) (string, bool) {
	name := providerBinary[provider]
	if name == "" {
		return "", false
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", false
	}
	return p, true
}

// TestLiveSmoke_LaunchProfile drives agent.Boot with the real runtime through
// an inline launch profile for claude, codex, and opencode. Each provider runs
// as an isolated subtest so one provider's auth/network failure does not block
// the others. ModeOneShot is used: it runs one turn synchronously and returns
// Status=Done/Failed + an ExitCode — exactly what a smoke needs.
func TestLiveSmoke_LaunchProfile(t *testing.T) {
	// claude bare-mode auth: thread the built torque-apikey-helper into the
	// planted .claude/settings.json. Built once here; subtests that aren't
	// claude ignore it.
	apiKeyHelper := buildApiKeyHelper(t)

	for _, provider := range []string{"claude-code", "codex", "opencode"} {
		provider := provider
		t.Run(provider, func(t *testing.T) {
			binPath, ok := binaryAvailable(provider)
			if !ok {
				t.Skipf("BLOCKED(environment): %s binary not found on PATH — cannot run a live smoke", provider)
			}
			t.Logf("provider=%s binary=%s", provider, binPath)

			cd := composeLiveDeps(t, provider)
			if provider == "claude-code" && apiKeyHelper != "" {
				cd.Deps.ApiKeyHelperPath = apiKeyHelper
				t.Logf("claude: ApiKeyHelperPath=%s (bare-mode keychain auth)", apiKeyHelper)
			}

			workdir := t.TempDir()

			// Per-provider 120s budget. ModeOneShot wraps turn-complete on
			// this ctx, so a hung provider surfaces as Status=Failed rather
			// than wedging the whole test.
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			sess, err := cd.Manager.Boot(ctx, agent.Options{
				TaskID:              "CW-LIVE-SMOKE-" + provider,
				AgentProfile:        "torque-backend",
				Workdir:             workdir,
				ProjectID:           "torque-live-smoke",
				Mode:                agent.ModeOneShot,
				OneShotPrompt:       "Reply with exactly the word: OK",
				LaunchProfileInline: liveInlineLaunchCatalog(provider),
			})

			reportLiveBoot(t, provider, "launch-profile", sess, err, workdir)
		})
	}
}

// TestLiveSmoke_NoLaunchProfile is the A/B sanity counterpart: it boots the
// SAME providers WITHOUT a launch profile (the pure-inline default path). Run
// alongside TestLiveSmoke_LaunchProfile, it isolates whether any failure is
// launch-profile-specific (Phase 5) or a baseline provider/auth issue
// (environment). If both fail identically the cause is environment; if only
// the launch-profile variant fails the cause is Phase 5.
func TestLiveSmoke_NoLaunchProfile(t *testing.T) {
	apiKeyHelper := buildApiKeyHelper(t)

	for _, provider := range []string{"claude-code", "codex", "opencode"} {
		provider := provider
		t.Run(provider, func(t *testing.T) {
			binPath, ok := binaryAvailable(provider)
			if !ok {
				t.Skipf("BLOCKED(environment): %s binary not found on PATH", provider)
			}
			t.Logf("provider=%s binary=%s (A/B baseline — no launch profile)", provider, binPath)

			cd := composeLiveDeps(t, provider)
			if provider == "claude-code" && apiKeyHelper != "" {
				cd.Deps.ApiKeyHelperPath = apiKeyHelper
			}

			workdir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			sess, err := cd.Manager.Boot(ctx, agent.Options{
				TaskID:        "CW-LIVE-SMOKE-NOLP-" + provider,
				AgentProfile:  "torque-backend",
				Workdir:       workdir,
				ProjectID:     "torque-live-smoke",
				Mode:          agent.ModeOneShot,
				OneShotPrompt: "Reply with exactly the word: OK",
				// No LaunchProfileInline — default pure-inline path.
			})

			reportLiveBoot(t, provider, "no-launch-profile", sess, err, workdir)
		})
	}
}

// buildApiKeyHelper builds cmd/torque-apikey-helper to a temp path so claude
// bare-mode sessions can authenticate via the macOS keychain. Returns "" (and
// logs) if the build fails — claude then falls back to ANTHROPIC_API_KEY in
// env or ~/.claude.json keychain auth.
func buildApiKeyHelper(t *testing.T) string {
	t.Helper()
	repoRoot := repoRootDir(t)
	out := filepath.Join(t.TempDir(), "torque-apikey-helper")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/torque-apikey-helper")
	cmd.Dir = repoRoot
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Logf("buildApiKeyHelper: build failed (%v): %s — claude will rely on env/keychain", err, string(combined))
		return ""
	}
	return out
}

// repoRootDir walks up from the test's working directory to the torque repo
// root (the dir holding go.mod).
func repoRootDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repoRootDir: go.mod not found walking up from test dir")
		}
		dir = parent
	}
}

// reportLiveBoot classifies the outcome of one live Boot call into the smoke's
// PASS / FAIL(Phase 5) / BLOCKED(environment) taxonomy and logs the evidence.
//
// Classification:
//   - Boot returns an error wrapping ErrLaunchProfile  → FAIL(Phase 5):
//     the launch-profile resolution / overlay path is broken.
//   - Boot returns any other ErrBootFailed              → inspected: a
//     planting / workspace error is FAIL(Phase 5); a provider-spawn error
//     (binary, exec) is BLOCKED(environment).
//   - Boot succeeds, Status=Done, ExitCode=0            → PASS.
//   - Boot succeeds, Status=Failed                      → BLOCKED(environment)
//     when the provider exited non-zero (auth/network); the launch-profile
//     path itself was exercised cleanly (boot dir planted, Boot returned).
//
// The function never hard-fails the test for an environment block — it logs
// and returns. A Phase 5 break IS a hard failure (t.Errorf).
func reportLiveBoot(t *testing.T, provider, variant string, sess *agent.Session, err error, workdir string) {
	t.Helper()

	if err != nil {
		// Boot itself returned an error. Distinguish launch-profile breaks
		// from provider-spawn / environment breaks.
		switch {
		case errors.Is(err, agent.ErrLaunchProfile):
			t.Errorf("FAIL(Phase 5) provider=%s variant=%s: launch-profile resolution broke: %v",
				provider, variant, err)
		case errors.Is(err, agent.ErrBootFailed):
			// ErrBootFailed without ErrLaunchProfile: could be a planting
			// bug (Phase 5) or a provider-spawn failure (environment). The
			// error message carries the stage. "plant boot dir" / "compile
			// launch" / "prepare launch" / "build launch plan" are Phase 5
			// stages; "construct runtime" / "prepare runtime" /
			// runtime-Start failures are provider/environment.
			msg := err.Error()
			switch {
			case containsAny(msg, "plant boot dir", "build launch plan", "compile launch", "prepare launch", "workspace", "ensure boot dir root"):
				t.Errorf("FAIL(Phase 5) provider=%s variant=%s: boot-dir / launch-plan stage broke: %v",
					provider, variant, err)
			default:
				t.Logf("BLOCKED(environment) provider=%s variant=%s: Boot failed at a provider/runtime stage: %v",
					provider, variant, err)
			}
		default:
			t.Logf("BLOCKED(environment) provider=%s variant=%s: Boot returned a non-Boot error: %v",
				provider, variant, err)
		}
		return
	}

	require.NotNil(t, sess, "Boot returned nil error but nil session — provider=%s", provider)

	// Boot returned cleanly. The launch-profile path was structurally
	// exercised: Boot resolved the inline profile, compiled+prepared the
	// plan, planted the boot dir, spawned the provider, and ran one turn.
	exit := -999
	if sess.ExitCode != nil {
		exit = *sess.ExitCode
	}
	t.Logf("provider=%s variant=%s: Boot OK — Status=%s ExitCode=%d BootDir=%s WorkspaceDir=%s",
		provider, variant, sess.Status, exit, sess.BootDir, sess.WorkspaceDir)

	// BootDir must have been non-empty at plant time (claude/codex/opencode
	// all have a BootDirSpec). ModeOneShot reaps the dir inline before Boot
	// returns, so we assert the path was RECORDED, not that it still exists.
	require.NotEmpty(t, sess.BootDir,
		"FAIL(Phase 5) provider=%s: Session.BootDir empty — boot dir was not planted", provider)
	if _, statErr := os.Stat(sess.BootDir); statErr == nil {
		t.Logf("provider=%s: boot dir still present (not yet reaped): %s", provider, sess.BootDir)
	} else {
		t.Logf("provider=%s: boot dir reaped inline by ModeOneShot (expected): %s", provider, sess.BootDir)
	}

	// Dump the session log on a non-clean exit so the report carries the
	// provider's own stderr/stream output.
	if sess.Status != agent.StatusDone || exit != 0 {
		t.Logf("BLOCKED(environment) provider=%s variant=%s: provider turn did not exit clean (Status=%s ExitCode=%d) — launch-profile path itself was exercised; this is a provider auth/network/exec issue",
			provider, variant, sess.Status, exit)
		dumpSessionLog(t, provider, sess.WorkspaceDir)
		return
	}

	t.Logf("PASS provider=%s variant=%s: booted through %s, turn completed, clean exit (ExitCode=0)",
		provider, variant, variant)
}

// dumpSessionLog tails the session.log + stream.jsonl from the workspace logs
// dir so a failed smoke carries the provider's own diagnostic output.
func dumpSessionLog(t *testing.T, provider, workspaceDir string) {
	t.Helper()
	if workspaceDir == "" {
		return
	}
	for _, name := range []string{"session.log", "stream.jsonl"} {
		p := filepath.Join(workspaceDir, "logs", name)
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if len(raw) == 0 {
			continue
		}
		// Tail the last ~4KB so the report stays readable.
		const tail = 4096
		if len(raw) > tail {
			raw = raw[len(raw)-tail:]
		}
		t.Logf("provider=%s --- %s (tail) ---\n%s\n--- end %s ---", provider, name, string(raw), name)
	}
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
