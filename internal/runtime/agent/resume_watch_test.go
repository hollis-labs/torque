package agent

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/providertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/writeq"
)

// The branches of the watch Manager.bootWithFreshFallback keeps on a resumed
// streaming-stdio session (CW-20261001-0202): the resume holds on its first
// content, the session ends, the grace passes, or the provider reports the
// session lost, including before the watch starts. Each test sets
// resumeLossGrace far above the bound it asserts, so a branch that does not
// return shows as the full grace, not as a pass.

const watchedResumeID = "00000000-0000-4000-8000-0000000000ff"

// slowMetaWriter delays the one session-meta write Boot makes after the
// wrapper session is ready and before it registers the session's handle, so
// a child that exits at once is already gone when Boot returns.
type slowMetaWriter struct {
	writeq.Writer
	delay time.Duration
}

func (w slowMetaWriter) Submit(ctx context.Context, name string, fn func(*sqlstore.WriteTx) error) error {
	if name == "agent_update_session_meta" {
		time.Sleep(w.delay)
	}
	return w.Writer.Submit(ctx, name, fn)
}

// watchedResume composes a Manager over a claude streaming profile, with
// resumeLossGrace set to grace, and the resume and fresh Options a caller of
// bootWithFreshFallback would hold.
func watchedResume(t *testing.T, grace, metaDelay time.Duration) (m *Manager, resume, fresh Options) {
	t.Helper()
	prev := resumeLossGrace
	resumeLossGrace = grace
	t.Cleanup(func() { resumeLossGrace = prev })

	store := newTestStoreForLongLived(t)
	deps := &Dependencies{
		Store:          store,
		StateWriter:    slowMetaWriter{Writer: writeq.NewDirect(store), delay: metaDelay},
		WorkspacesRoot: t.TempDir(),
		Profiles: config.ProfileMap{
			"test": {Executor: "cli", Provider: "claude-code", RuntimeKind: "streaming-stdio", PermissionMode: "acceptEdits"},
		},
	}
	m = NewManager(deps)
	deps.Sessions = m
	fresh = Options{Mode: ModeLongLived, AgentProfile: "test", Workdir: t.TempDir()}
	resume = fresh
	resume.ProviderSessionIDOverride = watchedResumeID
	return m, resume, fresh
}

// bootWatched runs the fallback under a bounded context and returns how long
// it took.
func bootWatched(t *testing.T, m *Manager, resume, fresh Options) (*Session, time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	start := time.Now()
	sess, err := m.bootWithFreshFallback(ctx, resume, fresh, "test")
	took := time.Since(start)
	t.Logf("bootWithFreshFallback returned after %v", took)
	require.NoError(t, err)
	require.NotNil(t, sess)
	t.Cleanup(func() { _ = m.Stop(context.Background(), sess.ID) })
	return sess, took
}

// firstTurnThen replays go-providers' captured claude stream, widening its
// first user turn to any text (Torque's kickoff is its own), and keeps the
// process alive afterwards.
func firstTurnThen(t *testing.T, then ...providertest.Step) providertest.Run {
	t.Helper()
	steps := providertest.FixtureSteps(t, "claude/stream_two_turns")
	require.GreaterOrEqual(t, len(steps), 6)
	script := append([]providertest.Step{providertest.RecvLine()}, steps[1:6]...)
	return providertest.Script(append(script, then...)...).When("--resume")
}

// A healthy resume that answers at once ends the watch long before the
// grace, and the session stays the resumed one.
func TestBootWithFreshFallback_HealthyResumeReturnsBeforeTheGrace(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude, firstTurnThen(t, providertest.AwaitEOF()), providertest.Script(providertest.AwaitEOF()))
	fake.Install()
	m, resume, fresh := watchedResume(t, 30*time.Second, 0)

	sess, took := bootWatched(t, m, resume, fresh)
	assert.Less(t, took, 10*time.Second, "a healthy resume returns at its first content, not at the grace")
	assert.True(t, sess.Resumed)
	assert.Len(t, fake.Calls(), 1, "no fresh boot")
}

// A healthy resume is known to hold from the CLI's init, not from its first
// content: a resumed worker can think for many seconds before it writes
// anything, and the watch must not hold Boot's caller for that. The fake
// answers with the init and then stays silent for longer than the bound; a
// watch that waits for content shows as that silence.
func TestBootWithFreshFallback_HealthyResumeReturnsAtTheInit(t *testing.T) {
	const silence = 6 * time.Second
	steps := providertest.FixtureSteps(t, "claude/stream_two_turns")
	require.GreaterOrEqual(t, len(steps), 6)
	require.NotNil(t, steps[1].Send, "step 1 is the system init")
	script := []providertest.Step{providertest.RecvLine(), steps[1], providertest.Sleep(silence)}
	script = append(script, steps[2:6]...)
	script = append(script, providertest.AwaitEOF())
	fake := providertest.New(t, runtimes.Claude, providertest.Script(script...).When("--resume"), providertest.Script(providertest.AwaitEOF()))
	fake.Install()
	m, resume, fresh := watchedResume(t, 30*time.Second, 0)

	sess, took := bootWatched(t, m, resume, fresh)
	assert.Less(t, took, silence/2, "the init shows the resume holds: the watch does not wait for content")
	assert.True(t, sess.Resumed)
	assert.Len(t, fake.Calls(), 1, "no fresh boot")
}

// A resumed child that exits on its first turn without reporting the session
// lost ends the watch at once: there is nothing left to wait for, and no
// loss to act on, so the ended session is what the caller gets, not a fresh
// boot and not the full grace.
func TestBootWithFreshFallback_ChildExitEndsTheWatch(t *testing.T) {
	fake := providertest.New(t, runtimes.Claude, providertest.Script(providertest.RecvLine(), providertest.Exit(1)).When("--resume"), providertest.Script(providertest.AwaitEOF()))
	fake.ExpectErrors()
	fake.Install()
	m, resume, fresh := watchedResume(t, 30*time.Second, 0)

	sess, took := bootWatched(t, m, resume, fresh)
	assert.Less(t, took, 10*time.Second, "the session ended: the watch does not wait out the grace")
	assert.True(t, sess.Resumed, "no loss was reported, so this is not a fresh boot")
	assert.Len(t, fake.Calls(), 1, "no fresh boot")
}

// A resumed session that stays silent is given the grace and no more: the
// session stays up as the resumed one.
func TestBootWithFreshFallback_SilenceIsBoundedByTheGrace(t *testing.T) {
	const grace = 400 * time.Millisecond
	fake := providertest.New(t, runtimes.Claude, providertest.Script(providertest.RecvLine(), providertest.AwaitEOF()).When("--resume"), providertest.Script(providertest.AwaitEOF()))
	fake.Install()
	m, resume, fresh := watchedResume(t, grace, 0)

	sess, took := bootWatched(t, m, resume, fresh)
	assert.GreaterOrEqual(t, took, grace, "the watch waits out the grace")
	assert.Less(t, took, 10*time.Second)
	assert.True(t, sess.Resumed)
	assert.Len(t, fake.Calls(), 1, "no fresh boot")
}

// A loss reported while Boot is still finishing is not dropped. The child
// exits on the lost session before Boot registers the wrapper handle, so the
// handle is gone (and the watch has nothing to wait on) by the time the
// watch looks: the loss has to be read off the signal, and the session boots
// fresh once.
func TestBootWithFreshFallback_LossBeforeTheWatchStartsBootsFresh(t *testing.T) {
	steps := providertest.FixtureSteps(t, "claude/stream_resume_unknown_id")
	require.NotEmpty(t, steps)
	lostRun := providertest.Script(append([]providertest.Step{providertest.RecvLine()}, steps[1:]...)...).When("--resume")
	fake := providertest.New(t, runtimes.Claude, lostRun, providertest.Script(providertest.AwaitEOF()))
	fake.ExpectErrors()
	fake.Install()
	m, resume, fresh := watchedResume(t, 30*time.Second, 750*time.Millisecond)

	sess, took := bootWatched(t, m, resume, fresh)
	assert.Less(t, took, 10*time.Second)
	assert.False(t, sess.Resumed, "the lost resume booted fresh")
	require.Eventually(t, func() bool { return len(fake.Calls()) >= 2 && len(fake.Calls()[1].Args) > 0 }, 5*time.Second, 20*time.Millisecond)
	calls := fake.Calls()
	require.Len(t, calls, 2, "one resume attempt, one fresh boot")
	assert.True(t, calls[0].HasArg("--resume"), "the resume was tried first: %v", calls[0].Args)
	assert.False(t, calls[1].HasArg("--resume"), "then one fresh boot: %v", calls[1].Args)
}
