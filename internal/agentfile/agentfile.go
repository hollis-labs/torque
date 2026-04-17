// Package agentfile loads per-task agent spec YAML files. An agent file
// defines the persona a task runs as: its system_prompt (required), an
// optional model override, extra environment variables, and a tools list
// (advisory in v1 — no enforcement yet).
//
// Files are referenced by tasks.agent_file (absolute or working_dir-relative).
// Callers validate existence at task create/update and parse contents at
// dispatch so stale files error the run — not the mutation.
package agentfile

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// AgentFile is the parsed v1 agent spec. Only SystemPrompt is required;
// all other fields fall through to profile defaults when unset.
type AgentFile struct {
	Name         string            `yaml:"name,omitempty"`
	Description  string            `yaml:"description,omitempty"`
	SystemPrompt string            `yaml:"system_prompt"`
	Tools        []string          `yaml:"tools,omitempty"`
	Model        string            `yaml:"model,omitempty"`
	Environment  map[string]string `yaml:"environment,omitempty"`
}

// Load reads and parses the given (already-resolved absolute) path.
// Returns an error when the file is missing, unreadable, malformed, or
// when the required system_prompt field is empty.
func Load(path string) (*AgentFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("agent file %s: %w", path, err)
	}
	var af AgentFile
	if err := yaml.Unmarshal(data, &af); err != nil {
		return nil, fmt.Errorf("agent file %s: parse: %w", path, err)
	}
	if af.SystemPrompt == "" {
		return nil, fmt.Errorf("agent file %s: system_prompt is required", path)
	}
	return &af, nil
}

// Resolve turns a task's agent_file field into an absolute filesystem path.
// An empty path returns "" with no error (no agent file set). An absolute
// path is returned verbatim. A relative path is joined against workingDir;
// if workingDir is empty, the relative path is rejected because it can't
// be anchored deterministically.
func Resolve(agentFilePath, workingDir string) (string, error) {
	if agentFilePath == "" {
		return "", nil
	}
	if filepath.IsAbs(agentFilePath) {
		return agentFilePath, nil
	}
	if workingDir == "" {
		return "", fmt.Errorf("relative agent_file %q requires working_dir to be set", agentFilePath)
	}
	return filepath.Join(workingDir, agentFilePath), nil
}

// Validate resolves and stats the agent file path, ensuring the file exists
// and is readable. It does NOT parse contents — that's the dispatch-time
// responsibility. An empty path is a no-op (no agent file set).
func Validate(agentFilePath, workingDir string) error {
	resolved, err := Resolve(agentFilePath, workingDir)
	if err != nil {
		return err
	}
	if resolved == "" {
		return nil
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("agent_file %s: %w", resolved, err)
	}
	if info.IsDir() {
		return fmt.Errorf("agent_file %s: is a directory, expected a file", resolved)
	}
	// Readability check: open for read then close.
	f, err := os.Open(resolved)
	if err != nil {
		return fmt.Errorf("agent_file %s: not readable: %w", resolved, err)
	}
	_ = f.Close()
	return nil
}
