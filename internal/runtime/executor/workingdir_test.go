package executor

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRequireExistingDir covers the three branches that classify a
// working_dir as a dispatch-permanent failure once it has been shape-
// resolved (CW-20260520-0003): empty (no-op, deferred to the executor),
// missing path (os.ErrNotExist), and an existing path that points at a
// regular file rather than a directory.
func TestRequireExistingDir(t *testing.T) {
	t.Run("empty is a no-op", func(t *testing.T) {
		if err := RequireExistingDir(""); err != nil {
			t.Fatalf("RequireExistingDir(\"\") = %v, want nil", err)
		}
	})

	t.Run("missing path returns ErrNotExist", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "definitely-not-here")
		err := RequireExistingDir(missing)
		if err == nil {
			t.Fatalf("expected error for missing path, got nil")
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("expected errors.Is(err, fs.ErrNotExist), got %v", err)
		}
	})

	t.Run("regular file is rejected as not-a-directory", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "not-a-dir.txt")
		if err := os.WriteFile(file, []byte("hi"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		err := RequireExistingDir(file)
		if err == nil {
			t.Fatalf("expected error for regular-file path, got nil")
		}
		if got, want := err.Error(), "not a directory"; !strings.Contains(got, want) {
			t.Fatalf("error %q does not contain %q", got, want)
		}
	})

	t.Run("real directory accepted", func(t *testing.T) {
		if err := RequireExistingDir(t.TempDir()); err != nil {
			t.Fatalf("RequireExistingDir(tempdir) = %v, want nil", err)
		}
	})
}
