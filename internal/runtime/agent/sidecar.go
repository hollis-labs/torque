package agent

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
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

// openStderrSidecar fans the spawned process's stderr into:
//
//  1. An in-memory tail buffer (used by the executor to surface the trailing
//     few KB on the result reason when a turn fails).
//  2. A per-run sidecar log at $TORQUE_DATA_DIR/runs/<run_id>.stderr.log
//     (preserving CW-20260417-0024 — RunID-scoped, useful for executor
//     ModeOneShot turns where each run has a unique RunID).
//  3. The per-session log at <workspaceLogPath> (typically
//     ~/.torque/workspaces/<proj>/<sess>/logs/session.log) when non-empty.
//     This is the path forensic tooling reaches for first when a session
//     fails, and the long-lived session boot path uses RunID=0 for every
//     spawn (because RunID is a scheduler-side concept), so without this tee
//     the session-scoped log stays empty and stderr would only be findable
//     in the (overwritten) 0.stderr.log file.
//
// All three sinks are best-effort: if any one fails to open we log a warning
// and continue with the rest. Losing stderr entirely is never acceptable; the
// tail buffer is always populated so the executor can still surface a tail
// in the failure reason.
//
// CW-20260508-0006: tee subprocess stderr into the session log so daemon-spawned
// claude (subprocess-per-turn) failures are visible in <workspace>/logs/session.log.
// Was: the runner only piped stderr to the per-run sidecar, leaving session.log
// empty for any RunID=0 boot (long-lived orchestrator/session-create paths).
//
// Forked from internal/runtime/cliexec/sidecar.go.
func openStderrSidecar(runID int64, workspaceLogPath string) (writer io.Writer, tail *tailBuffer, closer func()) {
	tail = newTailBuffer(stderrTailBytes)
	closers := make([]func(), 0, 2)
	writers := []io.Writer{tail}

	dataDir := os.Getenv("TORQUE_DATA_DIR")
	if dataDir == "" {
		dataDir = filepath.Join(os.TempDir(), "torque")
	}
	runsDir := filepath.Join(dataDir, "runs")

	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		log.Printf("agent: stderr sidecar dir unavailable (%s): %v — degrading run-sidecar to buffer only", runsDir, err)
	} else {
		sidecarPath := filepath.Join(runsDir, fmt.Sprintf("%d.stderr.log", runID))
		f, err := os.OpenFile(sidecarPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Printf("agent: stderr sidecar open failed (%s): %v — degrading run-sidecar to buffer only", sidecarPath, err)
		} else {
			writers = append(writers, f)
			closers = append(closers, func() { _ = f.Close() })
		}
	}

	if workspaceLogPath != "" {
		if err := os.MkdirAll(filepath.Dir(workspaceLogPath), 0o700); err != nil {
			log.Printf("agent: session-log dir unavailable (%s): %v — stderr will not be teed to session.log", filepath.Dir(workspaceLogPath), err)
		} else {
			f, err := os.OpenFile(workspaceLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				log.Printf("agent: session-log open failed (%s): %v — stderr will not be teed to session.log", workspaceLogPath, err)
			} else {
				writers = append(writers, f)
				closers = append(closers, func() { _ = f.Close() })
			}
		}
	}

	switch len(writers) {
	case 1:
		writer = tail
	default:
		writer = io.MultiWriter(writers...)
	}
	// Idempotent close (sync.Once): callers commonly use both `defer closer()`
	// for crash safety AND an explicit `closer()` before reading the files
	// back. Today the closer is naturally tolerant of double-call (each
	// inner Close just returns an "already closed" error that we discard),
	// but if this ever grows flush/rename/finalize logic the double-call
	// would become flaky. Wrap with sync.Once now to lock the invariant in.
	var once sync.Once
	closer = func() {
		once.Do(func() {
			for _, c := range closers {
				c()
			}
		})
	}
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
