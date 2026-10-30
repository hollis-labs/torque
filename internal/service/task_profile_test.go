package service_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

func profileCheck(known ...string) func(string, string) error {
	return func(launch, agent string) error {
		for _, n := range []string{launch, agent} {
			if n == "" {
				continue
			}
			ok := false
			for _, k := range known {
				ok = ok || k == n
			}
			if !ok {
				return errors.New("profile " + n + " does not resolve")
			}
		}
		return nil
	}
}

// CW-20261030-0207: unknown profiles are rejected at the service layer.
func TestTaskCreate_RejectsUnknownProfile(t *testing.T) {
	svc := setupService(t)
	svc.Task.SetProfileValidator(profileCheck("implementer"))

	_, err := svc.Task.Create(service.TaskCreateInput{Title: "t", AgentProfile: "tangent-worker-codex"})
	var verr *service.ValidationError
	require.True(t, errors.As(err, &verr), "got %v", err)
	assert.Equal(t, "agent_profile", verr.Field)
	assert.Contains(t, verr.Message, "tangent-worker-codex")

	_, err = svc.Task.Create(service.TaskCreateInput{Title: "t", LaunchProfile: "nope"})
	require.True(t, errors.As(err, &verr), "got %v", err)
	assert.Equal(t, "launch_profile", verr.Field)

	_, err = svc.Task.Create(service.TaskCreateInput{Title: "ok", AgentProfile: "implementer"})
	require.NoError(t, err)
	// No profile at all is fine: it means default.
	_, err = svc.Task.Create(service.TaskCreateInput{Title: "none"})
	require.NoError(t, err)
	// The mock executor never reads a profile.
	_, err = svc.Task.Create(service.TaskCreateInput{Title: "mock", Executor: "mock", AgentProfile: "mock"})
	require.NoError(t, err)
}

func TestTaskUpdate_RejectsUnknownProfile(t *testing.T) {
	svc := setupService(t)
	rec, err := svc.Task.Create(service.TaskCreateInput{Title: "t"})
	require.NoError(t, err)
	svc.Task.SetProfileValidator(profileCheck("implementer"))

	err = svc.Task.Update(rec.ID, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{AgentProfile: strp("typo")}})
	var verr *service.ValidationError
	require.True(t, errors.As(err, &verr), "got %v", err)
	assert.Equal(t, "agent_profile", verr.Field)

	require.NoError(t, svc.Task.Update(rec.ID, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{AgentProfile: strp("implementer")}}))
	// Clearing is allowed.
	require.NoError(t, svc.Task.Update(rec.ID, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{AgentProfile: strp("")}}))
}

func TestTemplate_RejectsAndInstantiateRejectsUnknownProfile(t *testing.T) {
	svc := setupService(t)
	svc.Task.SetProfileValidator(profileCheck("implementer"))

	_, err := svc.Template.Create(service.TemplateCreateInput{ID: "bad", Name: "bad", AgentProfile: "typo"})
	require.Error(t, err)

	_, err = svc.Template.Create(service.TemplateCreateInput{ID: "good", Name: "good", AgentProfile: "implementer"})
	require.NoError(t, err)

	_, err = svc.Template.Instantiate(service.TemplateInstantiateInput{
		TemplateID: "good", Title: "x", Overrides: map[string]any{"agent_profile": "typo"},
	})
	require.Error(t, err)
}
