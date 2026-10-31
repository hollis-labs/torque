package tetherexec

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/tetherclient"
)

// TestLiveScratchLaunch drives the real executor against a live Tether. It is
// skipped unless TORQUE_TETHER_LIVE_URL is set (http(s) or unix:/path), e.g.
//
//	TORQUE_TETHER_LIVE_URL=unix:/var/lib/fabric/tether/run/tetherd.sock \
//	TORQUE_TETHER_LIVE_LAUNCH=general-worker TORQUE_TETHER_LIVE_REAPER=20s \
//	go test ./internal/runtime/tetherexec -run Live -v
//
// It launches one real (billed) session with a trivial prompt.
func TestLiveScratchLaunch(t *testing.T) {
	url := os.Getenv("TORQUE_TETHER_LIVE_URL")
	if url == "" {
		t.Skip("TORQUE_TETHER_LIVE_URL not set")
	}
	launch := os.Getenv("TORQUE_TETHER_LIVE_LAUNCH")
	require.NotEmpty(t, launch)
	c := tetherclient.New(url, os.Getenv("TORQUE_TETHER_LIVE_TOKEN"))

	cap, err := c.Capacity(context.Background())
	require.NoError(t, err)
	t.Logf("capacity: %+v", cap)

	ex := New(c, nil, Config{PollInterval: 2 * time.Second, DefaultTimeout: 8 * time.Minute, StopGrace: 45 * time.Second})
	j := &executor.ExecutionJob{
		TaskID: "CW-LIVE-SCRATCH", TaskTitle: "scratch ping", RunID: time.Now().Unix(),
		Description: "Scratch connectivity test. Do not use any tools. Reply with exactly the single word PONG and nothing else.",
		Metadata: map[string]any{
			"tether_launch":                launch,
			"tether_stop_after_final_turn": envOr("TORQUE_TETHER_LIVE_REAPER", "20s"),
			"tether_route":                 map[string]any{"channel": "scratch", "kinds": []string{}},
		},
	}
	start := time.Now()
	r, err := ex.Run(context.Background(), j, func(e executor.ExecutionEvent) { t.Logf("%6.1fs %s", time.Since(start).Seconds(), e.Content) })
	require.NoError(t, err)
	t.Logf("result: status=%s reason=%q duration=%s", r.Status, r.Reason, time.Since(start))
	if r.TaskComment != nil {
		t.Logf("comment: %s", r.TaskComment.Content)
	}
	require.Equal(t, "done", r.Status)
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
