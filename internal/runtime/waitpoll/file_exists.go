package waitpoll

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// FileExists is a predicate that fires when path resolves to any existing
// filesystem entry (file, directory, symlink). params: {"path": "<path>"}.
type FileExists struct{}

// NewFileExists returns a FileExists predicate. The predicate holds no
// state; a singleton is fine.
func NewFileExists() *FileExists { return &FileExists{} }

func (p *FileExists) Type() string { return "file_exists" }

func (p *FileExists) Validate(params map[string]any) error {
	path, _ := params["path"].(string)
	if path == "" {
		return fmt.Errorf("file_exists: params.path required")
	}
	return nil
}

func (p *FileExists) Evaluate(_ context.Context, params map[string]any) (bool, error) {
	path, _ := params["path"].(string)
	_, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
