package gitexec_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/gitexec"
)

// isolateGit gives git an empty global config and no system config.
func isolateGit(t *testing.T) {
	t.Helper()
	global := filepath.Join(t.TempDir(), "gitconfig")
	require.NoError(t, os.WriteFile(global, nil, 0o600))
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "Test", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "Test", "GIT_COMMITTER_EMAIL": "t@t"} {
		t.Setenv(k, v)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

// plantedRepo is a repository whose own config names commands git would
// run, the way an agent with write access to it could plant them: an
// fsmonitor, a filter driver for every .txt file, and a post-checkout hook.
// Each touches its marker in marks when it runs.
func plantedRepo(t *testing.T) (repo, marks string) {
	t.Helper()
	repo, marks = t.TempDir(), t.TempDir()
	script := func(name string) string {
		path := filepath.Join(marks, name+".sh")
		body := "#!/bin/sh\ntouch " + filepath.Join(marks, name) + "\ncat\n"
		require.NoError(t, os.WriteFile(path, []byte(body), 0o700))
		return path
	}
	git(t, repo, "init", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "f.txt"), []byte("one\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("*.txt filter=planted\n"), 0o600))
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "seed")
	git(t, repo, "config", "core.fsmonitor", script("fsmonitor"))
	git(t, repo, "config", "filter.planted.clean", script("clean"))
	git(t, repo, "config", "filter.planted.smudge", script("smudge"))
	hook := filepath.Join(repo, ".git", "hooks", "post-checkout")
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+filepath.Join(marks, "hook")+"\n"), 0o700))
	return repo, marks
}

func ran(marks, name string) bool {
	_, err := os.Stat(filepath.Join(marks, name))
	return err == nil
}

func clearMarks(t *testing.T, marks string) {
	t.Helper()
	for _, name := range []string{"fsmonitor", "clean", "smudge", "hook"} {
		_ = os.Remove(filepath.Join(marks, name))
	}
}

// Planted config does not run when the daemon runs git: status, the
// worktree checkout, and worktree removal all leave every marker untouched,
// while plain git runs them (the control).
func TestCommandRunsNoPlantedCommand(t *testing.T) {
	isolateGit(t)
	ctx := context.Background()
	repo, marks := plantedRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "f.txt"), []byte("two\n"), 0o600))

	git(t, repo, "status", "--porcelain")
	require.True(t, ran(marks, "fsmonitor") && ran(marks, "clean"), "control: plain git status runs the planted fsmonitor and clean filter")
	wt := filepath.Join(t.TempDir(), "control")
	git(t, repo, "worktree", "add", "--detach", wt, "HEAD")
	require.True(t, ran(marks, "hook") && ran(marks, "smudge"), "control: plain git worktree add runs the planted hook and smudge filter")
	clearMarks(t, marks)

	out, err := gitexec.Command(ctx, repo, "status", "--porcelain").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "f.txt", "status still reports the change")
	wt = filepath.Join(t.TempDir(), "run")
	require.NoError(t, gitexec.Command(ctx, repo, "worktree", "add", "--detach", wt, "HEAD").Run())
	assert.FileExists(t, filepath.Join(wt, "f.txt"))
	require.NoError(t, gitexec.Command(ctx, repo, "worktree", "remove", wt).Run())
	for _, name := range []string{"fsmonitor", "clean", "smudge", "hook"} {
		assert.False(t, ran(marks, name), "the planted %s ran under the daemon's git", name)
	}
}

// Config that changes what a fetch runs is reported when the repository
// sets it, directly or through an include, and not when only the
// operator's own config does.
func TestLocalRemoteExecKey(t *testing.T) {
	isolateGit(t)
	ctx := context.Background()
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	git(t, repo, "remote", "add", "origin", "git@example.com:o/r.git")
	git(t, "", "config", "--global", "credential.helper", "store")

	key, err := gitexec.LocalRemoteExecKey(ctx, repo)
	require.NoError(t, err)
	assert.Empty(t, key, "the operator's global credential helper is theirs")

	for _, tc := range []struct{ key, value, want string }{
		{"core.sshCommand", "touch /tmp/x", "core.sshcommand"},
		{"credential.helper", "!touch /tmp/x", "credential.helper"},
		{"credential.https://example.com.helper", "!touch /tmp/x", "credential.https://example.com.helper"},
		{"remote.origin.uploadpack", "touch /tmp/x", "remote.origin.uploadpack"},
		{"url.ext::sh.insteadOf", "git@example.com", "url.ext::sh.insteadof"},
		{"protocol.ext.allow", "always", "protocol.ext.allow"},
		{"core.gitProxy", "touch /tmp/x", "core.gitproxy"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			git(t, repo, "config", tc.key, tc.value)
			defer git(t, repo, "config", "--unset", tc.key)
			key, err := gitexec.LocalRemoteExecKey(ctx, repo)
			require.NoError(t, err)
			assert.Equal(t, tc.want, key)
		})
	}

	t.Run("through an include", func(t *testing.T) {
		included := filepath.Join(t.TempDir(), "planted.cfg")
		require.NoError(t, os.WriteFile(included, []byte("[core]\n\tsshCommand = touch /tmp/x\n"), 0o600))
		git(t, repo, "config", "include.path", included)
		key, err := gitexec.LocalRemoteExecKey(ctx, repo)
		require.NoError(t, err)
		assert.Equal(t, "core.sshcommand", key)
	})
}
