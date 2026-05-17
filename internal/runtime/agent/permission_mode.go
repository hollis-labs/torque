package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/torque/internal/config"
)

// applyPermissionMode post-processes the planted .claude/settings.json so
// the spawned claude subprocess starts in the profile's PermissionMode
// (CW-20260517-0038 Variation 3 / P0).
//
// Background: go-providers' claudeSettingsStub renders
// `permissions.defaultMode: "bypassPermissions"` ONLY when the adapter's
// SkipPermissions flag is set (the dev / --dangerously-skip-permissions
// path). A non-dev profile therefore plants a settings.json with NO
// `permissions` block at all, leaving the spawned claude in its built-in
// `default` mode — every approval-needing tool then triggers an
// interactive prompt with no human at the TTY, and the run hangs.
//
// claudeSettingsStub's own godoc says apps may post-process the planted
// file for a richer permissions policy; this is that post-processing.
//
// Contract:
//
//   - bootDir == "" (adapter has no BootDirSpec — gemini/copilot today):
//     no-op, nil error. Nothing was planted.
//   - devModeBypass == true (the adapter already planted bypassPermissions
//     via SkipPermissions): no-op. Don't fight the dev path — it already
//     set the strongest posture, and re-merging would be a redundant write.
//   - otherwise: read <bootDir>/.claude/settings.json, merge
//     `permissions.defaultMode = <mode>` into the existing JSON object
//     (preserving apiKeyHelper and any other keys), and write it back.
//
// A missing settings.json is treated as an empty object — the merge still
// produces a valid file. Malformed JSON is a hard error (refuse to
// clobber an operator/adapter-authored file we can't safely parse).
func applyPermissionMode(bootDir string, mode config.PermissionMode, devModeBypass bool) error {
	if bootDir == "" {
		return nil
	}
	if devModeBypass {
		// The --dangerously-skip-permissions / SkipPermissions dev path
		// already planted permissions.defaultMode=bypassPermissions via
		// claudeSettingsStub. Leave it alone.
		return nil
	}
	if mode == "" {
		mode = config.DefaultPermissionMode
	}

	settingsPath := filepath.Join(bootDir, ".claude", "settings.json")

	settings := map[string]any{}
	raw, err := os.ReadFile(settingsPath)
	switch {
	case err == nil:
		if uerr := json.Unmarshal(raw, &settings); uerr != nil {
			return fmt.Errorf("apply permission mode: parse planted %s: %w", settingsPath, uerr)
		}
	case os.IsNotExist(err):
		// No planted file (or a render that produced nothing) — start
		// from an empty object so the merge still yields a valid file.
	default:
		return fmt.Errorf("apply permission mode: read planted %s: %w", settingsPath, err)
	}

	// Merge into the existing `permissions` object so a future stub that
	// emits permissions.allow / deny rules is preserved; only defaultMode
	// is asserted here.
	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	perms["defaultMode"] = string(mode)
	settings["permissions"] = perms

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("apply permission mode: marshal %s: %w", settingsPath, err)
	}
	out = append(out, '\n')

	// 0o600 mirrors the planted-file mode (the settings.json carries the
	// apiKeyHelper path; it must not be group/other-readable).
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		return fmt.Errorf("apply permission mode: ensure %s: %w", filepath.Dir(settingsPath), err)
	}
	if err := os.WriteFile(settingsPath, out, 0o600); err != nil {
		return fmt.Errorf("apply permission mode: write %s: %w", settingsPath, err)
	}
	return nil
}
