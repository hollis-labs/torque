package bootstrap_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/runtime/bootstrap"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/testutil/sessionpaths"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestSessionLaunchDaemonMCP_PathPolicy(t *testing.T) {
	root, cases := sessionpaths.Fixture(t)
	store := sqlitetest.OpenStore(t)
	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-ROOT", Name: "root", RepoPath: root, Status: "active"}))
	deps := &agent.Dependencies{Store: store, WorkspacesRoot: t.TempDir(), Profiles: config.ProfileMap{"default": {Provider: "claude-code"}}, SessionAllowedRoots: []string{}}
	mgr := agent.NewManager(deps)
	deps.Sessions = mgr
	srv := httptest.NewServer(bootstrap.DaemonMCPHandler(service.New(store), nil, mgr, nil))
	defer srv.Close()
	ctx := context.Background()
	client, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: srv.URL}, nil)
	require.NoError(t, err)
	defer client.Close()
	for _, tc := range cases {
		for _, field := range []string{"workdir", "repo_root"} {
			t.Run(tc.Name+"/"+field, func(t *testing.T) {
				result, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "torque_session_launch", Arguments: map[string]any{"agent_profile": "default", "workdir": root, "repo_root": root, field: tc.Path}})
				require.NoError(t, err)
				require.True(t, result.IsError, "unsafe launch was accepted")
				var text string
				for _, item := range result.Content {
					if c, ok := item.(*mcpsdk.TextContent); ok {
						text += c.Text
					}
				}
				require.Contains(t, text, "TORQUE_SESSION_ALLOWED_ROOTS")
				require.Contains(t, text, tc.Rule)
			})
		}
	}
	sessions, err := mgr.List("", "", "", 0)
	require.NoError(t, err)
	require.Empty(t, sessions, "refused requests created sessions")
}
