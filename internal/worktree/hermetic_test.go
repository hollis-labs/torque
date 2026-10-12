package worktree_test

import "os"

// hermeticGitEnv is the environment for every git the fixtures in this
// package run. It hides the developer's global and system git config (a
// global core.hooksPath, say, whose pre-push hook once blocked a push to a
// test's own temp bare repo) and pins the identity the commits need.
func hermeticGitEnv() []string {
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
}
