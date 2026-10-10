package sqlstore

import (
	"database/sql"
	"encoding/json"
	"strings"
)

// runProfileSnapshot is the only shape returned for a stored profile snapshot.
// Keep this allowlist in sync with the scheduler's write-time projection.
type runProfileSnapshot struct {
	Provider        string `json:"provider,omitempty"`
	Model           string `json:"model,omitempty"`
	Executor        string `json:"executor,omitempty"`
	RuntimeKind     string `json:"runtime_kind,omitempty"`
	PermissionMode  string `json:"permission_mode,omitempty"`
	LaunchProfileID string `json:"launch_profile_id,omitempty"`
	Role            string `json:"role,omitempty"`
	Tier            string `json:"tier,omitempty"`
}

// storedRunProfileSnapshot accepts both the current flat shape and the legacy
// CompiledLaunchProfile JSON shape. Secret-bearing and otherwise unknown fields
// are deliberately absent, so they cannot survive the projection.
type storedRunProfileSnapshot struct {
	runProfileSnapshot
	Profile struct {
		ID   string
		Role string
		Tier string
	} `json:"Profile"`
	AgentProfile struct {
		Provider       string
		Model          string
		Executor       string
		RuntimeKind    string
		PermissionMode string
	} `json:"AgentProfile"`
}

// ProjectRunProfileSnapshot returns a safe, allowlisted representation of a
// stored runs.profile_snapshot. Malformed, non-object, empty, and NULL values
// become an invalid sql.NullString and never echo their stored bytes.
func ProjectRunProfileSnapshot(stored sql.NullString) sql.NullString {
	if !stored.Valid || strings.TrimSpace(stored.String) == "" {
		return sql.NullString{}
	}

	var decoded storedRunProfileSnapshot
	if err := json.Unmarshal([]byte(stored.String), &decoded); err != nil {
		return sql.NullString{}
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stored.String), &object); err != nil || object == nil {
		return sql.NullString{}
	}

	projected := decoded.runProfileSnapshot
	if projected.Provider == "" {
		projected.Provider = decoded.AgentProfile.Provider
	}
	if projected.Model == "" {
		projected.Model = decoded.AgentProfile.Model
	}
	if projected.Executor == "" {
		projected.Executor = decoded.AgentProfile.Executor
	}
	if projected.RuntimeKind == "" {
		projected.RuntimeKind = decoded.AgentProfile.RuntimeKind
	}
	if projected.PermissionMode == "" {
		projected.PermissionMode = decoded.AgentProfile.PermissionMode
	}
	if projected.LaunchProfileID == "" {
		projected.LaunchProfileID = decoded.Profile.ID
	}
	if projected.Role == "" {
		projected.Role = decoded.Profile.Role
	}
	if projected.Tier == "" {
		projected.Tier = decoded.Profile.Tier
	}

	b, err := json.Marshal(projected)
	if err != nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(b), Valid: true}
}

func projectRunProfileSnapshot(r *RunRecord) {
	r.ProfileSnapshot = ProjectRunProfileSnapshot(r.ProfileSnapshot)
}
