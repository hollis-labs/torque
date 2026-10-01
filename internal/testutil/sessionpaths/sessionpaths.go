// Package sessionpaths supplies isolated adversarial launch paths for surface tests.
package sessionpaths

import (
	"os"
	"path/filepath"
	"testing"
)

type Case struct{ Name, Path, Rule string }

func Fixture(t *testing.T) (string, []Case) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, "dev", "repo")
	outside := t.TempDir()
	dirs := []string{root, filepath.Join(home, ".tether", "catalog"), filepath.Join(home, "tether", "state"), filepath.Join(home, ".config", "torque"), filepath.Join(home, ".local", "share", "torque"), filepath.Join(home, ".local", "state", "torque"), filepath.Join(home, ".claude-cache"), filepath.Join(home, ".codex-extra"), filepath.Join(home, ".gemini-test"), root + "-sibling"}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{"outside-link": outside, "control-link": dirs[1]} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	cases := []Case{{"outside", outside, "outside allowed bases"}, {"sibling", root + "-sibling", "outside allowed bases"}, {"symlink-outside", filepath.Join(root, "outside-link"), "outside allowed bases"}, {"symlink-control", filepath.Join(root, "control-link"), "control-plane"}, {"traversal", root + "/../../.tether/catalog", "control-plane"}, {"home", home, "operator home"}, {"ancestor", filepath.Dir(home), "operator home"}, {"relative", "repo", "existing absolute directory"}}
	for i, d := range dirs[1:9] {
		cases = append(cases, Case{Name: []string{"catalog", "tether-state", "config", "local-share", "local-state", "claude-prefix", "codex-prefix", "gemini-prefix"}[i], Path: d, Rule: "control-plane"})
	}
	cases = append(cases, Case{"control-ancestor", filepath.Join(home, ".local"), "control-plane"})
	return root, cases
}
