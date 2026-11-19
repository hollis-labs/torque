package sqlstore_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/stretchr/testify/require"
)

// Independent handles exercise the database locking boundary, not just a Go mutex.
func TestProjectExternalReferenceConcurrentCreates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "references.db")
	var stores []*sqlstore.Store
	for i := 0; i < 2; i++ {
		db, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		require.NoError(t, migrations.Run(db))
		store, err := sqlstore.New(db, "sqlite")
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, store.Close()) })
		stores = append(stores, store)
	}
	const count = 24
	type outcome struct {
		record  *sqlstore.ProjectRecord
		created bool
		err     error
	}
	outcomes := make(chan outcome, count)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			p, created, err := stores[i%2].CreateProjectByReference(&sqlstore.ProjectRecord{Name: "shared", RepoPath: "/repo", ExternalAuthority: "registry", ExternalID: "shared"})
			outcomes <- outcome{p, created, err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(outcomes)
	var id string
	created := 0
	for out := range outcomes {
		require.NoError(t, out.err)
		if id == "" {
			id = out.record.ID
		}
		require.Equal(t, id, out.record.ID)
		if out.created {
			created++
		}
	}
	require.Equal(t, 1, created)
	// Different references racing must each get a distinct sequential project ID.
	outcomes = make(chan outcome, count)
	start = make(chan struct{})
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			p, created, err := stores[i%2].CreateProjectByReference(&sqlstore.ProjectRecord{Name: "shared", RepoPath: "/repo", ExternalAuthority: "registry", ExternalID: fmt.Sprint(i)})
			outcomes <- outcome{p, created, err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(outcomes)
	ids := map[string]bool{}
	for out := range outcomes {
		require.NoError(t, out.err)
		require.True(t, out.created)
		require.False(t, ids[out.record.ID])
		ids[out.record.ID] = true
	}
	projects, err := stores[0].ListProjects(sqlstore.ProjectFilter{})
	require.NoError(t, err)
	require.Len(t, projects, count+1)
	for _, p := range projects {
		require.Equal(t, "registry", p.ExternalAuthority)
		require.NotEmpty(t, p.ExternalID)
	}
}

func TestProjectExternalReferenceConcurrentConflict(t *testing.T) {
	store := setupTestStore(t)
	type outcome struct {
		record  *sqlstore.ProjectRecord
		created bool
		err     error
	}
	for _, field := range []string{"name", "repo_path"} {
		t.Run(field, func(t *testing.T) {
			start := make(chan struct{})
			results := make(chan outcome, 2)
			for i := 0; i < 2; i++ {
				go func(i int) {
					<-start
					p := &sqlstore.ProjectRecord{Name: "original", RepoPath: "/original", ExternalAuthority: "registry", ExternalID: field}
					if i == 1 {
						if field == "name" {
							p.Name = "different"
						} else {
							p.RepoPath = "/different"
						}
					}
					record, created, err := store.CreateProjectByReference(p)
					results <- outcome{record, created, err}
				}(i)
			}
			close(start)
			var winner *sqlstore.ProjectRecord
			conflicts := 0
			for i := 0; i < 2; i++ {
				r := <-results
				if r.err != nil {
					require.ErrorIs(t, r.err, sqlstore.ErrProjectReferenceConflict)
					conflicts++
				} else {
					require.True(t, r.created)
					winner = r.record
				}
			}
			require.Equal(t, 1, conflicts)
			require.NotNil(t, winner)
			got, err := store.GetProjectByReference("registry", field)
			require.NoError(t, err)
			require.Equal(t, winner, got)
		})
	}
}
