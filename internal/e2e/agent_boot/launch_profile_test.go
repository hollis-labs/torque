package agent_boot

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// TestBoot_LaunchProfile_RoutesThroughResolver pins the new launch-profile
// resolution path. A Boot request that carries only LaunchProfile must
// produce a Session row stamped with both fields (LaunchProfile = the new
// selector, AgentProfile = the resolver-derived registry key).
func TestBoot_LaunchProfile_RoutesThroughResolver(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:        "CW-TEST-LP-001",
		LaunchProfile: "default",
		Workdir:       t.TempDir(),
		Mode:          agent.ModeLongLived,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	assert.Equal(t, "default", sess.LaunchProfile,
		"resolved launch_profile must land on Session.LaunchProfile")
	assert.Equal(t, "default", sess.AgentProfile,
		"resolver must derive AgentProfile from the launch family's lookup key")

	// Verify the same fields landed on the persisted session row.
	row, err := cd.Store.GetSession(sess.ID)
	require.NoError(t, err)
	assert.Equal(t, "default", row.LaunchProfile,
		"sessions.launch_profile column must persist the resolved launch family ID")
	assert.Equal(t, "default", row.AgentProfile)
}

// TestBoot_LaunchProfile_PrecedenceOverLegacy confirms the resolver's
// precedence rule: when both LaunchProfile and AgentProfile are supplied,
// LaunchProfile wins. The legacy AgentProfile is ignored on the wire even
// though the registry still has a matching entry.
func TestBoot_LaunchProfile_PrecedenceOverLegacy(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:        "CW-TEST-LP-002",
		LaunchProfile: "worker.implementer", // builtin → maps to "default" registry key
		AgentProfile:  "torque-backend",     // present but must be ignored
		Workdir:       t.TempDir(),
		Mode:          agent.ModeLongLived,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	assert.Equal(t, "worker.implementer", sess.LaunchProfile,
		"explicit LaunchProfile must win over legacy AgentProfile")
	assert.Equal(t, "default", sess.AgentProfile,
		"resolver must derive AgentProfile from the launch family, not from "+
			"the legacy AgentProfile field, when LaunchProfile is set")
}

// TestBoot_LegacyAgentProfile_StillBoots is the migration-safety guarantee:
// existing rows / requests carrying only AgentProfile (no LaunchProfile)
// continue to boot via the legacy-compat path. The resolver maps known
// legacy names onto builtin families; unknown names synthesize a pass-
// through profile keyed by the legacy name verbatim.
func TestBoot_LegacyAgentProfile_StillBoots(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// "torque-backend" is in the test registry but NOT in the legacy-map
	// table — exercises the legacy_passthrough provenance branch.
	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:       "CW-TEST-LP-003",
		AgentProfile: "torque-backend",
		Workdir:      t.TempDir(),
		Mode:         agent.ModeLongLived,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	assert.Equal(t, "torque-backend", sess.AgentProfile)
	assert.Equal(t, "torque-backend", sess.LaunchProfile,
		"legacy_passthrough provenance must surface the passthrough profile "+
			"ID (= the legacy name) on Session.LaunchProfile so downstream "+
			"consumers always see a non-empty selector")
}

// TestBoot_LaunchProfile_PlantsTaskBundle covers the load-bearing regression:
// the launch-profile refactor must not regress task-bundle planting. We
// inspect the planted boot dir on disk — agentsessions.preparePlant
// materializes the BootDirSpec normally because the fake runtime captures
// the adapter and replays the planting walk.
func TestBoot_LaunchProfile_PlantsTaskBundle(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: true}, "claude-code")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:        "CW-TEST-LP-004",
		TaskTitle:     "Plant bundle via launch_profile",
		TaskKind:      "agent",
		TaskStatus:    "doing",
		LaunchProfile: "worker.implementer",
		Workdir:       t.TempDir(),
		Mode:          agent.ModeLongLived,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.NotEmpty(t, sess.BootDir, "Boot must capture the planted boot dir")

	taskMD, found := findPlantedFile(sess.BootDir, "task.md")
	require.True(t, found,
		"planted task bundle (tasks/<id>/task.md) must exist under boot dir %s",
		sess.BootDir)
	body, err := os.ReadFile(taskMD)
	require.NoError(t, err)
	contents := string(body)
	assert.Contains(t, contents, "CW-TEST-LP-004",
		"planted task.md must carry the task id")
	assert.Contains(t, contents, "Plant bundle via launch_profile",
		"planted task.md must carry the task title")
}

// findPlantedFile walks the per-session boot dir looking for the first
// file whose basename matches. Used to assert the task-bundle layout
// without binding the test to the exact agentkit injection rel-path.
func findPlantedFile(root, basename string) (string, bool) {
	var hit string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, string(filepath.Separator)+basename) {
			hit = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil || hit == "" {
		return "", false
	}
	return hit, true
}
