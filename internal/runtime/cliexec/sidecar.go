package cliexec

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

// openStderrSidecar returns an io.Writer that fans the spawned process's
// stderr into both an in-memory tail buffer and a per-run sidecar log at
// $CLOCKWORK_DATA_DIR/runs/<run_id>.stderr.log (preserving CW-20260417-0024).
// If the sidecar can't be opened we degrade to buffer-only and log a warning;
// losing stderr entirely is never acceptable.
//
// The returned closer must be invoked after the process exits to flush + close
// the sidecar file.
func openStderrSidecar(runID int64) (writer io.Writer, tail *bytes.Buffer, closer func()) {
	tail = &bytes.Buffer{}
	closer = func() {}

	dataDir := os.Getenv("CLOCKWORK_DATA_DIR")
	if dataDir == "" {
		dataDir = filepath.Join(os.TempDir(), "clockwork")
	}
	runsDir := filepath.Join(dataDir, "runs")

	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		log.Printf("cliexec: stderr sidecar dir unavailable (%s): %v — using buffer only", runsDir, err)
		return tail, tail, closer
	}

	sidecarPath := filepath.Join(runsDir, fmt.Sprintf("%d.stderr.log", runID))
	f, err := os.OpenFile(sidecarPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("cliexec: stderr sidecar open failed (%s): %v — using buffer only", sidecarPath, err)
		return tail, tail, closer
	}

	writer = io.MultiWriter(tail, f)
	closer = func() { _ = f.Close() }
	return writer, tail, closer
}

// tailString returns the trailing up-to-max bytes of buf, trimmed of
// surrounding whitespace.
func tailString(buf *bytes.Buffer, max int) string {
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
