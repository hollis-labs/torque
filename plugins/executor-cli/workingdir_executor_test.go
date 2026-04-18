package executorcli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRun_WorkingDirTildeExpands is the end-to-end proof that the fix in
// CW-20260418-0014 works: constructing a job with working_dir="~/…" must
// produce a run that actually chdirs into the expanded absolute path and
// completes successfully. Before this fix, os/exec.Cmd.Dir received the
// literal "~/" and the kernel returned ENOENT — see the symptom in the
// ticket ("chdir ~/Projects-apps/nanite: no such file or directory").
func TestRun_WorkingDirTildeExpands(t *testing.T) {
	// Point HOME at a controlled directory so the `~/` expansion has a real
	// target we can assert on. t.Setenv is goroutine-safe within the test and
	// restores HOME on return.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	targetSub := "tmp/clockwork-test-wd"
	targetAbs := filepath.Join(tmpHome, targetSub)
	require.NoError(t, os.MkdirAll(targetAbs, 0o755))

	// Script prints `pwd` so we can verify the actual cwd the shell landed
	// in, then emits CLOCKWORK_DONE so the executor treats the run as a
	// success.
	script := `pwd
echo CLOCKWORK_DONE`
	pm := profiles("default", shellProfile(script))
	e := New(pm)
	j := job("default")
	j.WorkingDir = "~/" + targetSub // the failure-shape input

	var logLines []string
	result, err := e.Run(context.Background(), j, func(ev executor.ExecutionEvent) {
		if ev.Type == executor.EventLog {
			logLines = append(logLines, ev.Content)
		}
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "done", result.Status, "run must succeed now that ~/ expands")

	require.NotEmpty(t, logLines, "expected pwd output on stdout")
	// On macOS /tmp is a symlink to /private/tmp; Clean+Abs does NOT follow
	// symlinks, but `pwd` in the shell does. Resolving both sides via
	// EvalSymlinks lets us compare the two observations fairly without
	// teaching the resolver to follow symlinks (which we explicitly don't).
	observedPwd := logLines[0]
	wantAbsResolved, _ := filepath.EvalSymlinks(targetAbs)
	gotAbsResolved, _ := filepath.EvalSymlinks(observedPwd)
	assert.Equal(t, wantAbsResolved, gotAbsResolved,
		"shell pwd (%s) should resolve to the expanded HOME path (%s)", observedPwd, targetAbs)
}

// TestRun_WorkingDirUnresolvableIsPermanent is the block-no-retry proof:
// a job whose working_dir references a form the resolver rejects
// (~nonexistent-user/...) MUST surface a PermanentError so the scheduler
// path in CW-20260418-0010 blocks the task on first attempt without
// consuming the retry budget. We can't reach the scheduler from this unit
// test, so we assert on the executor's contract directly: Run returns a
// PermanentError AND a blocked-shape result so both surfaces (the
// scheduler's err path and the run row) carry the same signal.
func TestRun_WorkingDirUnresolvableIsPermanent(t *testing.T) {
	pm := profiles("default", shellProfile(`echo CLOCKWORK_DONE`))
	e := New(pm)
	j := job("default")
	j.WorkingDir = "~nonexistent-user/path"

	result, err := e.Run(context.Background(), j, nil)
	require.Error(t, err, "unresolvable working_dir must return an error from Run")
	assert.True(t, executor.IsPermanent(err),
		"unresolvable working_dir must be PermanentError so scheduler blocks-no-retry")
	require.NotNil(t, result)
	assert.Equal(t, "blocked", result.Status,
		"result must mirror the block so the run row carries a blocked status")
	assert.Contains(t, result.Reason, "working_dir",
		"reason must mention working_dir so operator can diagnose")
}

// TestValidate_WorkingDirUnresolvableIsPermanent proves the pre-dispatch hot
// path: when the scheduler asks Validate whether a task can be dispatched,
// an unresolvable working_dir MUST return a PermanentError so the scheduler
// routes the task directly to handlePermanentValidationError (status=blocked,
// retry_count unchanged, at most one audit run row). This is the hot path
// the ticket's symptom ("4 retries in 48h on a single task") was meant to
// cure — catching the bad path before any process is spawned.
func TestValidate_WorkingDirUnresolvableIsPermanent(t *testing.T) {
	pm := profiles("default", shellProfile(`echo CLOCKWORK_DONE`))
	e := New(pm)
	j := job("default")
	j.WorkingDir = "~nonexistent-user/path"

	err := e.Validate(j)
	require.Error(t, err)
	assert.True(t, executor.IsPermanent(err),
		"unresolvable working_dir from Validate must be PermanentError")
	assert.Contains(t, err.Error(), "working_dir")
}

// TestValidate_WorkingDirUndefinedEnvIsPermanent covers the other
// block-no-retry variant: a working_dir like "$PROJ_ROOT/…" where the env
// var isn't exported in the scheduler's environment. os.ExpandEnv would
// have silently become "/…" — we reject instead.
func TestValidate_WorkingDirUndefinedEnvIsPermanent(t *testing.T) {
	// Paranoia: in case a previous test leaked the var, explicitly unset.
	t.Setenv("CW_WD_DEFINITELY_UNSET_VAR", "")
	os.Unsetenv("CW_WD_DEFINITELY_UNSET_VAR")

	pm := profiles("default", shellProfile(`echo CLOCKWORK_DONE`))
	e := New(pm)
	j := job("default")
	j.WorkingDir = "$CW_WD_DEFINITELY_UNSET_VAR/path"

	err := e.Validate(j)
	require.Error(t, err)
	assert.True(t, executor.IsPermanent(err),
		"undefined env var in working_dir must surface PermanentError")
	assert.Contains(t, err.Error(), "is not set")
}

// TestValidate_WorkingDirEmptyIsAllowed confirms the intentional
// backward-compat carve-out: a task with no working_dir set must still
// pass Validate so the executor can run in its process cwd. Several
// non-project-bound agent templates (smoke probes, status reporters)
// rely on this.
func TestValidate_WorkingDirEmptyIsAllowed(t *testing.T) {
	pm := profiles("default", shellProfile(`echo CLOCKWORK_DONE`))
	e := New(pm)
	j := job("default")
	j.WorkingDir = ""

	assert.NoError(t, e.Validate(j),
		"empty working_dir must be allowed — executor falls back to process cwd")
}

// TestValidate_WorkingDirRelativeIsPermanent guards the strict policy
// decision: relative paths in working_dir are rejected rather than
// silently resolved against the scheduler's cwd. Silent resolution is an
// orchestrator footgun (the scheduler may run from anywhere — systemd,
// a launchd agent, a user shell — and each cwd produces different task
// behavior).
func TestValidate_WorkingDirRelativeIsPermanent(t *testing.T) {
	pm := profiles("default", shellProfile(`echo CLOCKWORK_DONE`))
	e := New(pm)
	j := job("default")
	j.WorkingDir = "some/relative/path"

	err := e.Validate(j)
	require.Error(t, err)
	assert.True(t, executor.IsPermanent(err),
		"relative working_dir must surface PermanentError")
	assert.Contains(t, err.Error(), "is relative")
}
