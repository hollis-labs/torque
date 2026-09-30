package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A client-supplied forwarding header must never make a remote caller look
// local (CW-20260930-0224), and a loopback peer relaying someone else (a
// same-host proxy) is not local either.
func TestAdminGate_ForwardingHeadersNeverGrantLocal(t *testing.T) {
	t.Setenv("TORQUE_ADMIN_TOKEN", "")
	cases := []struct {
		name   string
		remote string
		header string
		value  string
	}{
		{"remote spoofs X-Real-IP", "203.0.113.9:5000", "X-Real-IP", "127.0.0.1"},
		{"remote spoofs X-Forwarded-For", "203.0.113.9:5000", "X-Forwarded-For", "127.0.0.1"},
		{"remote spoofs Forwarded", "203.0.113.9:5000", "Forwarded", "for=127.0.0.1"},
		{"remote spoofs ipv6 loopback", "203.0.113.9:5000", "X-Real-IP", "::1"},
		{"loopback proxy relaying remote", "127.0.0.1:5000", "X-Forwarded-For", "203.0.113.9"},
		{"loopback proxy claiming loopback", "127.0.0.1:5000", "X-Real-IP", "127.0.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			h := adminGate(func(w http.ResponseWriter, _ *http.Request) { called = true })
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.RemoteAddr = tc.remote
			req.Header.Set(tc.header, tc.value)
			rr := httptest.NewRecorder()
			h(rr, req)
			if rr.Code != http.StatusForbidden || called {
				t.Fatalf("code=%d called=%v, want 403 and handler not run", rr.Code, called)
			}
		})
	}
}

// Through the real router: before the fix, middleware.RealIP rewrote
// RemoteAddr from X-Real-IP ahead of the gate. The rebuild cooldown is
// armed first so a regression answers 429 instead of running npm.
func TestAdminRoute_SpoofedRealIPRefused(t *testing.T) {
	t.Setenv("TORQUE_ADMIN_TOKEN", "")
	restartFrontendMu.Lock()
	prev := restartFrontendLast
	restartFrontendLast = time.Now()
	restartFrontendMu.Unlock()
	t.Cleanup(func() {
		restartFrontendMu.Lock()
		restartFrontendLast = prev
		restartFrontendMu.Unlock()
	})

	s := New(nil, nil)
	for _, header := range []string{"X-Real-IP", "X-Forwarded-For"} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/restart-frontend", nil)
		req.Host = "127.0.0.1:8990" // pass the tokenless loopback-Host check
		req.RemoteAddr = "203.0.113.9:5000"
		req.Header.Set(header, "127.0.0.1")
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s spoof: code=%d body=%s, want 403", header, rr.Code, rr.Body.String())
		}
	}
}
