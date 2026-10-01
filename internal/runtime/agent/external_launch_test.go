package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/testutil/sessionpaths"
)

func TestExternalLaunchPaths_AllowlistAndDenylist(t *testing.T) {
	root, cases := sessionpaths.Fixture(t)
	m := NewManager(&Dependencies{SessionAllowedRoots: []string{root}})
	for _, tc := range cases {
		for _, field := range []string{"workdir", "repo_root"} {
			t.Run(tc.Name+"/"+field, func(t *testing.T) {
				opts := Options{Workdir: root, RepoRoot: root}
				if field == "workdir" {
					opts.Workdir = tc.Path
				} else {
					opts.RepoRoot = tc.Path
				}
				_, err := m.constrainExternalLaunch(opts)
				if !errors.Is(err, ErrLaunchPathRefused) || !strings.Contains(err.Error(), tc.Rule) || !strings.Contains(err.Error(), SessionAllowedRootsEnv) {
					t.Fatalf("refusal=%v", err)
				}
			})
		}
	}
	// A permitted alias is normalized before it becomes a launch root.
	alias := filepath.Join(filepath.Dir(root), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	opts, err := m.constrainExternalLaunch(Options{Workdir: alias})
	if err != nil || opts.Workdir != root || opts.RepoRoot != root {
		t.Fatalf("normalized=%+v err=%v", opts, err)
	}
	// A configured base cannot override control-plane denial.
	m.sessionAllowedRoots = []string{filepath.Dir(root), os.Getenv("HOME")}
	_, err = m.constrainExternalLaunch(Options{Workdir: filepath.Join(os.Getenv("HOME"), ".tether", "catalog")})
	if !errors.Is(err, ErrLaunchPathRefused) || !strings.Contains(err.Error(), "control-plane") {
		t.Fatal(err)
	}
}

func TestExternalLaunchPaths_ProjectsUnionOperatorRootsAndNoDefault(t *testing.T) {
	root, _ := sessionpaths.Fixture(t)
	t.Setenv(SessionAllowedRootsEnv, "")
	store := newGateTestStore(t)
	m := NewManager(&Dependencies{Store: store})
	if _, err := m.constrainExternalLaunch(Options{Workdir: root}); !errors.Is(err, ErrLaunchPathRefused) || !strings.Contains(err.Error(), "no usable allowed bases") {
		t.Fatalf("no-default=%v", err)
	}
	if err := store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-ROOT", Name: "root", RepoPath: root, Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.constrainExternalLaunch(Options{Workdir: root}); err != nil {
		t.Fatal(err)
	}
	extra := t.TempDir()
	t.Setenv(SessionAllowedRootsEnv, extra)
	m = NewManager(&Dependencies{Store: store})
	// Mutating the process environment later must not expand this manager's grants.
	late := t.TempDir()
	t.Setenv(SessionAllowedRootsEnv, late)
	for _, allowed := range []string{root, extra} {
		if _, err := m.constrainExternalLaunch(Options{Workdir: allowed}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.constrainExternalLaunch(Options{Workdir: late}); !errors.Is(err, ErrLaunchPathRefused) {
		t.Fatal("late environment changed grants", err)
	}
}
