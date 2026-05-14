package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminGate_NonLocalhostDenied(t *testing.T) {
	h := adminGate(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "203.0.113.9:54321"
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-loopback, got %d", rr.Code)
	}
}

func TestAdminGate_LocalhostAllowedWithoutToken(t *testing.T) {
	t.Setenv("TORQUE_ADMIN_TOKEN", "")
	called := false
	h := adminGate(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK || !called {
		t.Fatalf("expected handler to run; code=%d called=%v", rr.Code, called)
	}
}

func TestAdminGate_TokenMismatchDenied(t *testing.T) {
	t.Setenv("TORQUE_ADMIN_TOKEN", "secret")
	h := adminGate(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Admin-Token", "wrong")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 with wrong token, got %d", rr.Code)
	}
}

func TestAdminGate_TokenMatchAllowed(t *testing.T) {
	t.Setenv("TORQUE_ADMIN_TOKEN", "secret")
	called := false
	h := adminGate(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "[::1]:12345"
	req.Header.Set("X-Admin-Token", "secret")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK || !called {
		t.Fatalf("expected handler to run; code=%d called=%v", rr.Code, called)
	}
}
