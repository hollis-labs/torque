package mcpadapter_test

import (
	"context"
	"testing"

	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestStandaloneMCPCommentAcknowledges(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	svc := service.New(store)
	adapter := mcpadapter.New(svc, nil)
	r, err := store.SubmitRequest(context.Background(), sqlstore.RequestSubmission{RequesterURN: "msg://user/local/owner", RecipientURN: "msg://agent/local/worker", IdempotencyKey: "mcp-claim", Subject: "Work"})
	require.NoError(t, err)
	text, isErr := callTool(t, adapter, "torque_comment_add", map[string]interface{}{"entity_id": r.TaskID, "entity_type": "task", "author": "worker", "content": "STATUS started · standalone MCP"})
	require.False(t, isErr, text)
	got, err := store.GetRequest(context.Background(), r.TaskID)
	require.NoError(t, err)
	require.NotNil(t, got.AckAt)
	assert.Equal(t, "acknowledged", got.State)
}
