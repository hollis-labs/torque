package service

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
)

// ProjectExecutionDefaults are the task defaults a project supplies
// (CW-20261030-0208). Precedence when a task is created is
// task > template > project > global: a value the task or its template sets
// wins, the project fills what is still unset, and the global defaults
// (on_done=review, max_retries=3, no gates, the default profile) cover the rest.
//
// A template's on_done and max_retries are stored with the global defaults
// when the template never set them, so instantiation treats a template value
// equal to the global default as "unset" and lets the project default apply.
type ProjectExecutionDefaults struct {
	Executor      string   `json:"executor,omitempty"`
	LaunchProfile string   `json:"launch_profile,omitempty"`
	AgentProfile  string   `json:"agent_profile,omitempty"`
	OnDone        string   `json:"on_done,omitempty"`
	MaxRetries    *int     `json:"max_retries,omitempty"`
	QualityGates  []string `json:"quality_gates,omitempty"`
}

// Global task defaults, mirrored from Create.
const (
	globalOnDone     = "review"
	globalMaxRetries = 3
)

// ParseProjectExecutionDefaults decodes the stored column; NULL/empty is the
// zero value. Unknown keys are rejected so a typo does not silently do nothing.
func ParseProjectExecutionDefaults(raw sql.NullString) (ProjectExecutionDefaults, error) {
	var d ProjectExecutionDefaults
	if !raw.Valid || raw.String == "" {
		return d, nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(raw.String)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return d, &ValidationError{Field: "execution_defaults", Message: "invalid execution_defaults: " + err.Error() +
			" (keys: executor, launch_profile, agent_profile, on_done, max_retries, quality_gates)"}
	}
	return d, nil
}

// validateExecutionDefaults checks a project's defaults the way the same
// fields are checked on a task.
func (s *ProjectService) validateExecutionDefaults(raw sql.NullString, tasks *TaskService) error {
	d, err := ParseProjectExecutionDefaults(raw)
	if err != nil {
		return err
	}
	if d.OnDone != "" && !validOnDone[d.OnDone] {
		return &ValidationError{Field: "execution_defaults.on_done", Message: fmt.Sprintf("invalid on_done %q: use close, review or notify", d.OnDone)}
	}
	if d.MaxRetries != nil && *d.MaxRetries < 0 {
		return &ValidationError{Field: "execution_defaults.max_retries", Message: "max_retries must be >= 0"}
	}
	if err := validateQualityGates(d.QualityGates); err != nil {
		return err
	}
	if tasks != nil {
		if err := tasks.validateExecutor(d.Executor); err != nil {
			return err
		}
		if err := tasks.validateProfiles(d.Executor, d.LaunchProfile, d.AgentProfile); err != nil {
			return err
		}
	}
	return nil
}

// applyProjectDefaults fills the task input's still-unset execution fields from
// its project. A project that is missing or has no defaults changes nothing.
func (s *TaskService) applyProjectDefaults(input *TaskCreateInput) {
	if input.ProjectID == "" {
		return
	}
	proj, err := s.store.GetProject(input.ProjectID)
	if err != nil {
		return
	}
	d, err := ParseProjectExecutionDefaults(proj.ExecutionDefaults)
	if err != nil {
		return
	}
	applyExecutionDefaults(input, d)
}

func applyExecutionDefaults(input *TaskCreateInput, d ProjectExecutionDefaults) {
	if input.Executor == "" {
		input.Executor = d.Executor
	}
	// The profile pair is one decision: a task that names either keeps its own.
	if input.LaunchProfile == "" && input.AgentProfile == "" {
		input.LaunchProfile = d.LaunchProfile
		input.AgentProfile = d.AgentProfile
	}
	if input.OnDone == "" {
		input.OnDone = d.OnDone
	}
	if input.MaxRetries == nil && d.MaxRetries != nil {
		v := *d.MaxRetries
		input.MaxRetries = &v
	}
	if len(input.QualityGates) == 0 && len(d.QualityGates) > 0 {
		input.QualityGates = append([]string(nil), d.QualityGates...)
	}
}
