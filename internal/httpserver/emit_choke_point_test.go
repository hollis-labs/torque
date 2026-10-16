package httpserver_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// State kinds must reach /events through the outbox (sse_outbox.go) and nowhere
// else, or one transition would be published twice, or by one path and not
// another (CW-20261016-0133). This is a code-shape guard on purpose: the way
// the old gap opened was a handler broadcasting by hand.
func TestStateKindsAreNotBroadcastOutsideTheOutbox(t *testing.T) {
	banned := []string{
		`"task.transitioned"`, `"task.state_changed"`,
		`"checkpoint.emitted"`, `"checkpoint.responded"`, `"checkpoint.canceled"`,
		// the plugin bus constants for the same kind
		`plugin.EventTaskTransitioned`, `EmitTaskTransitioned(`,
	}
	for _, dir := range []string{".", "../mcpadapter", "../service"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			// sse_bridge.go skips the bus copy of task.transitioned by name.
			if strings.HasSuffix(f, "_test.go") || filepath.Base(f) == "sse_outbox.go" || filepath.Base(f) == "sse_bridge.go" {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, b := range banned {
				if strings.Contains(string(src), b) {
					t.Errorf("%s mentions %s: state events are emitted only by the outbox writer (sqlstore/state_events.go) and served by sse_outbox.go", f, b)
				}
			}
		}
	}
}
