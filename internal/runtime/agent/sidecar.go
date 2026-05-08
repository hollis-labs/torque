package agent

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

// stderrTailBytes caps how much stderr we surface in result.Reason on failure.
// Full stream still flows to the sidecar file; this is the inline tail.
const stderrTailBytes = 8 * 1024

type tailBuffer struct {
	buf []byte
	max int
}

func newTailBuffer(max int) *tailBuffer {
	return &tailBuffer{max: max}
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	if t.max <= 0 {
		return len(p), nil
	}
	if len(p) >= t.max {
		t.buf = append(t.buf[:0], p[len(p)-t.max:]...)
		return len(p), nil
	}
	need := len(t.buf) + len(p) - t.max
	if need > 0 {
		t.buf = append([]byte(nil), t.buf[need:]...)
	}
	t.buf = append(t.buf, p...)
	return len(p), nil
}

func (t *tailBuffer) Bytes() []byte {
	return t.buf
}

// openStderrSidecar fans the spawned process's stderr into both an in-memory
// tail buffer and a per-run sidecar log at $CLOCKWORK_DATA_DIR/runs/<run_id>.stderr.log
// (preserving CW-20260417-0024). If the sidecar can't be opened we degrade
// to buffer-only and log a warning; losing stderr entirely is never acceptable.
//
// Forked from internal/runtime/cliexec/sidecar.go without semantic change.
func openStderrSidecar(runID int64) (writer io.Writer, tail *tailBuffer, closer func()) {
	tail = newTailBuffer(stderrTailBytes)
	closer = func() {}

	dataDir := os.Getenv("CLOCKWORK_DATA_DIR")
	if dataDir == "" {
		dataDir = filepath.Join(os.TempDir(), "clockwork")
	}
	runsDir := filepath.Join(dataDir, "runs")

	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		log.Printf("agent: stderr sidecar dir unavailable (%s): %v — using buffer only", runsDir, err)
		return tail, tail, closer
	}

	sidecarPath := filepath.Join(runsDir, fmt.Sprintf("%d.stderr.log", runID))
	f, err := os.OpenFile(sidecarPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("agent: stderr sidecar open failed (%s): %v — using buffer only", sidecarPath, err)
		return tail, tail, closer
	}

	writer = io.MultiWriter(tail, f)
	closer = func() { _ = f.Close() }
	return writer, tail, closer
}

// tailString returns the trailing up-to-max bytes of buf, trimmed of
// surrounding whitespace.
func tailString(buf *tailBuffer, max int) string {
	if buf == nil {
		return ""
	}
	data := buf.Bytes()
	if len(data) == 0 {
		return ""
	}
	if len(data) > max {
		data = data[len(data)-max:]
	}
	return string(bytes.TrimSpace(data))
}
