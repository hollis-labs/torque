package service_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

func TestTemplateService_Create_BasicRoundTrip(t *testing.T) {
	svc := setupService(t)

	tpl, err := svc.Template.Create(service.TemplateCreateInput{
		ID:          "backend-fix",
		Name:        "Backend Fix",
		Description: "Stock backend bug fix",
		Kind:        "agent",
		AutoExecute: true,
		Executor:    "cli",
	})
	require.NoError(t, err)
	assert.Equal(t, "backend-fix", tpl.ID)
	assert.Equal(t, 1, tpl.Version)
	assert.Equal(t, "agent", tpl.Kind)
}

func TestTemplateService_Create_InvalidKind_422(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Template.Create(service.TemplateCreateInput{
		ID: "x", Name: "x", Kind: "nonsense", Description: "x",
	})
	require.Error(t, err)
	var verr *service.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "kind", verr.Field)
}

func TestTemplateService_Create_RequiresID(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Template.Create(service.TemplateCreateInput{Name: "x", Description: "x"})
	require.Error(t, err)
	var verr *service.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "id", verr.Field)
}

func TestTemplateService_Update_AppendsNewVersion(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Template.Create(service.TemplateCreateInput{
		ID: "t", Name: "v1", Description: "x", Kind: "agent", Executor: "cli", AutoExecute: true,
	})
	require.NoError(t, err)

	tpl, err := svc.Template.Update("t", service.TemplateUpdateInput{Name: "v2"})
	require.NoError(t, err)
	assert.Equal(t, 2, tpl.Version)
	assert.Equal(t, "v2", tpl.Name)
	// Inherited.
	assert.Equal(t, "agent", tpl.Kind)
	assert.Equal(t, "cli", tpl.Executor.String)
}

func TestTemplateService_Update_UnknownTemplate_422(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Template.Update("missing", service.TemplateUpdateInput{Name: "v2"})
	require.Error(t, err)
	var verr *service.ValidationError
	require.ErrorAs(t, err, &verr)
}

func TestTemplateService_Delete_Referenced_Conflict(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Template.Create(service.TemplateCreateInput{
		ID: "t", Name: "t", Description: "x", Kind: "agent", Executor: "cli", AutoExecute: true,
	})
	require.NoError(t, err)

	// Instantiate so a task references the template.
	_, err = svc.Template.Instantiate(service.TemplateInstantiateInput{
		TemplateID: "t", Title: "ref", Description: "x",
	})
	require.NoError(t, err)

	err = svc.Template.Delete("t")
	require.Error(t, err)
	var cerr *service.ConflictError
	require.ErrorAs(t, err, &cerr)

	// Message should be human-readable and actionable; must NOT echo the
	// sentinel's .Error() text ("template has referencing tasks") into the
	// wrapped body, because that produces the previous
	// "conflict: smoke-bug: 1 referencing tasks: template has referencing tasks"
	// double-stutter. See CW-20260416-0001 (post-MVP smoke finding).
	msg := cerr.Error()
	assert.Equal(t, 1, strings.Count(msg, "referencing"),
		"delete-conflict message should not echo 'referencing' twice, got: %q", msg)
	assert.Contains(t, msg, "archive instead of deleting",
		"message should steer the caller toward archive: %q", msg)
	// "template t" is the tokenized form the Delete() formatter produces —
	// bare "t" would false-positive against the word "template" itself.
	assert.Contains(t, msg, "template t ", "message should name the template id: %q", msg)
}

func TestTemplateService_Archive(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Template.Create(service.TemplateCreateInput{
		ID: "t", Name: "t", Description: "x", Kind: "agent", Executor: "cli", AutoExecute: true,
	})
	require.NoError(t, err)
	require.NoError(t, svc.Template.Archive("t", 1))

	tpl, err := svc.Template.Get("t", 1)
	require.NoError(t, err)
	assert.True(t, tpl.IsArchived)
}

// Templates can now set task.WorkingDir directly via the typed column
// (migration 010, decision from CW-20260416-0003). The value supports
// {{var}} resolution just like description/system_prompt/environment.
func TestTemplateService_Instantiate_WorkingDir_ResolvesVars(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Template.Create(service.TemplateCreateInput{
		ID:           "backend-fix",
		Name:         "Backend Fix",
		Description:  "x",
		Kind:         "agent",
		Executor:     "cli",
		AutoExecute:  true,
		WorkingDir:   "{{repo_path}}",
		RequiredVars: []string{"repo_path"},
	})
	require.NoError(t, err)

	task, err := svc.Template.Instantiate(service.TemplateInstantiateInput{
		TemplateID: "backend-fix",
		Title:      "fix auth",
		Vars:       map[string]string{"repo_path": "/tmp/repo"},
	})
	require.NoError(t, err)
	assert.Equal(t, "/tmp/repo", task.WorkingDir,
		"WorkingDir should be resolved from the template's typed column, not metadata")
}

// Templates without working_dir set should leave task.WorkingDir as its
// caller/default value.
func TestTemplateService_Instantiate_NoWorkingDir_TaskWorkingDirEmpty(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Template.Create(service.TemplateCreateInput{
		ID: "plain", Name: "p", Description: "x",
		Kind: "agent", Executor: "cli", AutoExecute: true,
	})
	require.NoError(t, err)

	task, err := svc.Template.Instantiate(service.TemplateInstantiateInput{
		TemplateID: "plain", Title: "t",
	})
	require.NoError(t, err)
	assert.Equal(t, "", task.WorkingDir)
}

// Unresolved {{var}} in working_dir should be a 422 like the other
// templatable fields, not a silent half-resolved path.
func TestTemplateService_Instantiate_WorkingDir_UnresolvedVar_422(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Template.Create(service.TemplateCreateInput{
		ID: "bad", Name: "b", Description: "x",
		Kind: "agent", Executor: "cli", AutoExecute: true,
		WorkingDir: "{{never}}",
	})
	require.NoError(t, err)

	_, err = svc.Template.Instantiate(service.TemplateInstantiateInput{
		TemplateID: "bad", Title: "t",
	})
	require.Error(t, err)
	var verr *service.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "working_dir", verr.Field)
}

// Update flow: append a new version changing working_dir.
func TestTemplateService_Update_WorkingDir(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Template.Create(service.TemplateCreateInput{
		ID: "t", Name: "v1", Description: "x",
		Kind: "agent", Executor: "cli", AutoExecute: true,
		WorkingDir: "/v1",
	})
	require.NoError(t, err)

	wd := "/v2"
	tpl, err := svc.Template.Update("t", service.TemplateUpdateInput{
		WorkingDir: &wd,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, tpl.Version)
	assert.True(t, tpl.WorkingDir.Valid)
	assert.Equal(t, "/v2", tpl.WorkingDir.String)
}

func TestTemplateService_Instantiate_ResolvesVarsAndStampsTemplateRef(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Template.Create(service.TemplateCreateInput{
		ID:           "backend-fix",
		Name:         "Backend Fix",
		Description:  "Fix {{issue}} in {{repo_path}}",
		Kind:         "agent",
		AutoExecute:  true,
		Executor:     "cli",
		RequiredVars: []string{"issue", "repo_path"},
		MetadataTemplate: map[string]any{
			"working_dir_template": "{{repo_path}}",
			"nested": map[string]any{
				"issue": "{{issue}}",
			},
		},
	})
	require.NoError(t, err)

	task, err := svc.Template.Instantiate(service.TemplateInstantiateInput{
		TemplateID:  "backend-fix",
		Title:       "Fix the auth bug",
		Description: "",
		Vars: map[string]string{
			"issue":     "auth-42",
			"repo_path": "/tmp/repo",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "agent", task.Kind)
	assert.Equal(t, "cli", task.Executor)

	// Description resolved from template because in.Description was empty.
	assert.Equal(t, "Fix auth-42 in /tmp/repo", task.Description)

	// template_ref stamped + metadata resolved.
	require.True(t, task.Metadata.Valid)
	var md map[string]any
	require.NoError(t, json.Unmarshal([]byte(task.Metadata.String), &md))
	ref := md["template_ref"].(map[string]any)
	assert.Equal(t, "backend-fix", ref["id"])
	assert.Equal(t, float64(1), ref["version"])
	assert.Equal(t, "/tmp/repo", md["working_dir_template"])
	nested := md["nested"].(map[string]any)
	assert.Equal(t, "auth-42", nested["issue"])
}

func TestTemplateService_Instantiate_MissingVar_422(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Template.Create(service.TemplateCreateInput{
		ID:           "x",
		Name:         "x",
		Description:  "x",
		Kind:         "agent",
		Executor:     "cli",
		AutoExecute:  true,
		RequiredVars: []string{"a"},
	})
	require.NoError(t, err)

	_, err = svc.Template.Instantiate(service.TemplateInstantiateInput{
		TemplateID:  "x",
		Title:       "t",
		Description: "d",
		// Vars missing "a"
	})
	require.Error(t, err)
	var verr *service.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Contains(t, verr.Field, "a")
}

func TestTemplateService_Instantiate_UnresolvedPlaceholder_422(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Template.Create(service.TemplateCreateInput{
		ID:          "x",
		Name:        "x",
		Description: "Need {{missing}}",
		Kind:        "agent",
		Executor:    "cli",
		AutoExecute: true,
	})
	require.NoError(t, err)

	_, err = svc.Template.Instantiate(service.TemplateInstantiateInput{
		TemplateID: "x", Title: "t",
	})
	require.Error(t, err)
	var verr *service.ValidationError
	require.ErrorAs(t, err, &verr)
}

func TestTemplateService_Instantiate_Overrides(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Template.Create(service.TemplateCreateInput{
		ID:          "t", Name: "t", Description: "x",
		Kind: "agent", Executor: "cli", AutoExecute: true,
	})
	require.NoError(t, err)

	task, err := svc.Template.Instantiate(service.TemplateInstantiateInput{
		TemplateID:  "t",
		Title:       "overridden",
		Description: "runtime desc",
		Overrides: map[string]any{
			"executor": "api",
			"priority": 1,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "api", task.Executor)
	assert.Equal(t, 1, task.Priority)
	assert.Equal(t, "runtime desc", task.Description)
}

func TestTemplateService_Instantiate_LatestVersionByDefault(t *testing.T) {
	svc := setupService(t)

	_, err := svc.Template.Create(service.TemplateCreateInput{
		ID: "t", Name: "v1", Description: "x",
		Kind: "agent", Executor: "cli", AutoExecute: true,
	})
	require.NoError(t, err)
	_, err = svc.Template.Update("t", service.TemplateUpdateInput{Description: "v2"})
	require.NoError(t, err)

	task, err := svc.Template.Instantiate(service.TemplateInstantiateInput{
		TemplateID: "t", Title: "from latest",
	})
	require.NoError(t, err)

	var md map[string]any
	require.NoError(t, json.Unmarshal([]byte(task.Metadata.String), &md))
	ref := md["template_ref"].(map[string]any)
	assert.Equal(t, float64(2), ref["version"])
	assert.Equal(t, "v2", task.Description)
}
