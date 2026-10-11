package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlannerImportCommand(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TORQUE_DB_PATH", filepath.Join(tmp, "torque.db"))
	t.Setenv("TORQUE_QUEUE_DB_PATH", filepath.Join(tmp, "queue.db"))

	data := filepath.Join(tmp, "data")
	require.NoError(t, os.Mkdir(data, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(data, "inbox.json"),
		[]byte(`{"schema":"portfolio/inbox@1","items":[{"id":"IN-001","title":"t","rev":2}]}`), 0o644))

	var out bytes.Buffer
	require.NoError(t, runPlannerImport(context.Background(), &out, data, false, false, false))
	assert.Contains(t, out.String(), "DRY RUN")

	out.Reset()
	require.NoError(t, runPlannerImport(context.Background(), &out, data, true, false, false))
	assert.Contains(t, out.String(), "Verified")
	assert.Contains(t, out.String(), "features.planner is off")

	out.Reset()
	require.NoError(t, runPlannerImport(context.Background(), &out, data, true, false, true))
	assert.Contains(t, out.String(), `"verified": true`)

	require.Error(t, runPlannerImport(context.Background(), &out, "", false, false, false))
}
