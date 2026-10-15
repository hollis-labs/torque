package httpserver

import (
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// defaultActivityURL is where the Chimera host serving the activity-monitor
// plugin listens (loopback, no auth). Override with TORQUE_ACTIVITY_URL.
const defaultActivityURL = "http://127.0.0.1:18099"

const activitySnapshotPath = "/api/plugins/activity-monitor/agents"

// maxActivityBody bounds what is relayed from the activity host.
const maxActivityBody = 4 << 20

var activityClient = &http.Client{Timeout: 3 * time.Second}

// activityAgents relays the activity-monitor snapshot to the browser, so the
// GUI reaches the loopback-only Chimera host through Torque's own /api/v1
// auth policy and holds no address or credential of its own. The body is
// passed through verbatim; the agent fields are untrusted free text.
func (s *Server) activityAgents(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimRight(os.Getenv("TORQUE_ACTIVITY_URL"), "/")
	if base == "" {
		base = defaultActivityURL
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, base+activitySnapshotPath, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "activity source misconfigured: "+err.Error())
		return
	}
	resp, err := activityClient.Do(req)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "activity source unavailable at "+base)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, "activity source returned "+resp.Status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	io.Copy(w, io.LimitReader(resp.Body, maxActivityBody)) //nolint:errcheck
}
