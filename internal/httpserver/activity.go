package httpserver

import (
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

// defaultActivityURL is where the Chimera host serving the activity-monitor
// plugin listens (loopback, no auth). Override with TORQUE_ACTIVITY_URL.
const defaultActivityURL = "http://127.0.0.1:8099"

const activityStreamPath = "/api/stream"

const activitySnapshotPath = "/api/plugins/activity-monitor/agents"

// maxActivityBody bounds what is relayed from the activity host.
const maxActivityBody = 4 << 20

var activityClient = &http.Client{Timeout: 3 * time.Second}

// activityAgents relays the activity-monitor snapshot to the browser, so the
// GUI reaches the loopback-only Chimera host through Torque's own /api/v1
// auth policy and holds no address or credential of its own. The body is
// passed through verbatim; the agent fields are untrusted free text.
func activityBase() string {
	if base := strings.TrimRight(os.Getenv("TORQUE_ACTIVITY_URL"), "/"); base != "" {
		return base
	}
	return defaultActivityURL
}

func (s *Server) activityAgents(w http.ResponseWriter, r *http.Request) {
	base := activityBase()
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

// activityStream relays the host's SSE stream. Only the resume cursor (?since
// or Last-Event-ID) and the ?kind filter reach the source; nothing else from
// the browser request does. FlushInterval -1 flushes every write, so events
// and keepalives are not held back by the proxy.
func (s *Server) activityStream(w http.ResponseWriter, r *http.Request) {
	target, err := url.Parse(activityBase())
	if err != nil || target.Host == "" {
		writeError(w, http.StatusInternalServerError, "activity source misconfigured")
		return
	}
	q := url.Values{}
	for _, k := range []string{"since", "kind"} {
		if v := r.URL.Query().Get(k); v != "" {
			q.Set(k, v)
		}
	}
	rp := &httputil.ReverseProxy{
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = target.Scheme
			pr.Out.URL.Host = target.Host
			pr.Out.Host = target.Host
			pr.Out.URL.Path = activityStreamPath
			pr.Out.URL.RawQuery = q.Encode()
			pr.Out.Header = http.Header{"Accept": {"text/event-stream"}}
			if id := r.Header.Get("Last-Event-ID"); id != "" {
				pr.Out.Header.Set("Last-Event-ID", id)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			writeError(w, http.StatusServiceUnavailable, "activity source unavailable at "+target.Host)
		},
	}
	w.Header().Set("Cache-Control", "no-store")
	rp.ServeHTTP(w, r)
}
