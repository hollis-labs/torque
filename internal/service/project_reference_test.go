package service_test

import (
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
)

func TestProjectExternalReferenceReplayAfterRepositoryRemoved(t *testing.T) {
	svc := setupService(t)
	require.NoError(t, svc.Feature.Enable("projects"))
	repo := t.TempDir()
	input := service.ProjectCreateInput{Name: "project", RepoPath: repo, ExternalAuthority: "registry", ExternalID: "project:tether"}
	first, err := svc.Project.CreateWithResult(input)
	require.NoError(t, err)
	require.True(t, first.Created)
	require.NoError(t, os.Remove(repo))
	found, err := svc.Project.CreateWithResult(input)
	require.NoError(t, err)
	require.True(t, found.Found)
	require.False(t, found.Created)
	require.Equal(t, first.ID, found.ID)
	input.Name = "other"
	_, err = svc.Project.CreateWithResult(input)
	var conflict *service.ConflictError
	require.ErrorAs(t, err, &conflict)
}

func TestProjectExternalReferenceServicePartialPair(t *testing.T) {
	svc := setupService(t)
	require.NoError(t, svc.Feature.Enable("projects"))
	for _, ref := range [][2]string{{"registry", ""}, {"", "id"}, {" ", "id"}, {"registry", " "}} {
		_, err := svc.Project.Create(service.ProjectCreateInput{Name: "project", RepoPath: t.TempDir(), ExternalAuthority: ref[0], ExternalID: ref[1]})
		var invalid *service.ValidationError
		require.ErrorAs(t, err, &invalid)
		_, err = svc.Project.GetByReference(ref[0], ref[1])
		require.ErrorAs(t, err, &invalid)
	}
}
