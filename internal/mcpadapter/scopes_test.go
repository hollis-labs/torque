package mcpadapter

import (
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/mcpscopes"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
)

// Deny by default: every tool Torque can register has a scope, so no tool
// is refused for want of a mapping once auth enforces, and none is left open.
func TestEveryToolHasAScope(t *testing.T) {
	svc := setupServiceForScopes(t)
	for _, f := range []string{"sprints", "projects", "epics", "collections", "planner"} {
		require.NoError(t, svc.Feature.Enable(f))
	}
	defs := New(svc, nil).Server().ToolDefinitions()
	require.NotEmpty(t, defs)
	var unmapped []string
	for _, d := range defs {
		if _, ok := mcpscopes.Tools[d.Name]; !ok {
			unmapped = append(unmapped, d.Name)
		}
	}
	sort.Strings(unmapped)
	assert.Empty(t, unmapped, "registered tools with no entry in internal/mcpscopes: add each one deliberately (read/write/admin)")
}

// The table and the annotation table cover the same tools, and a tool is
// torque.read exactly when it is annotated read-only.
func TestToolScopesMatchAnnotations(t *testing.T) {
	for name, h := range toolAnnotations {
		scope, ok := mcpscopes.Tools[name]
		if !assert.True(t, ok, "%s has annotations but no scope", name) {
			continue
		}
		assert.Equal(t, h.ReadOnly, scope == mcpscopes.Read, "%s: read-only hint %v but scope %s", name, h.ReadOnly, scope)
	}
	for name, scope := range mcpscopes.Tools {
		_, ok := toolAnnotations[name]
		assert.True(t, ok, "%s is scoped but is not a known tool (stale entry)", name)
		assert.Contains(t, []string{mcpscopes.Read, mcpscopes.Write, mcpscopes.Admin}, scope, name)
	}
}

func setupServiceForScopes(t *testing.T) *service.Service {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return service.New(store)
}

// Tools served by the same handler do the same thing, so they must share a
// scope; otherwise the weaker alias bypasses the stronger one
// (torque_session_create is torque_session_launch under another name).
func TestToolsSharingAHandlerShareAScope(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	re := regexp.MustCompile(`newTool\("(torque_[a-z_]+)",(?s:.*?)\), a\.(handle\w+)\)`)
	byHandler := map[string][]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			byHandler[m[2]] = append(byHandler[m[2]], m[1])
		}
	}
	require.NotEmpty(t, byHandler)
	for h, tools := range byHandler {
		for _, tool := range tools[1:] {
			assert.Equal(t, mcpscopes.Tools[tools[0]], mcpscopes.Tools[tool], "%s and %s share %s but not a scope", tools[0], tool, h)
		}
	}
	assert.Equal(t, mcpscopes.Admin, mcpscopes.Tools["torque_session_create"])
	assert.Equal(t, mcpscopes.Admin, mcpscopes.Tools["torque_session_launch"])
}
