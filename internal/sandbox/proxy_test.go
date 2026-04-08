package sandbox

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// proxyClient returns an http.Client configured to use the given proxy address.
func proxyClient(proxyAddr string) *http.Client {
	transport := &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			// Use the proxy for all requests
			return &url.URL{
				Scheme: "http",
				Host:   proxyAddr,
			}, nil
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func TestProxy_AllowedHTTP(t *testing.T) {
	// Start a test backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello from backend"))
	}))
	defer backend.Close()

	// Extract host from backend URL (e.g., "127.0.0.1")
	backendHost, _, _ := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))

	proxy := NewProxy([]string{backendHost})
	if err := proxy.Start(); err != nil {
		t.Fatalf("proxy start: %v", err)
	}
	defer proxy.Stop()

	client := proxyClient(proxy.Addr)
	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatalf("GET error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "hello from backend") {
		t.Errorf("unexpected body: %s", body)
	}
}

func TestProxy_DeniedHTTP(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	// Allow only example.com — not the backend
	proxy := NewProxy([]string{"example.com"})
	if err := proxy.Start(); err != nil {
		t.Fatalf("proxy start: %v", err)
	}
	defer proxy.Stop()

	client := proxyClient(proxy.Addr)
	resp, err := client.Get(backend.URL)
	if err != nil {
		// Some clients return an error on 403
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

func TestProxy_ConnectAllowed(t *testing.T) {
	// Start a TLS test server
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("tls ok"))
	}))
	defer backend.Close()

	backendHost, _, _ := net.SplitHostPort(strings.TrimPrefix(backend.URL, "https://"))

	proxy := NewProxy([]string{backendHost})
	if err := proxy.Start(); err != nil {
		t.Fatalf("proxy start: %v", err)
	}
	defer proxy.Stop()

	client := proxyClient(proxy.Addr)
	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatalf("CONNECT error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestProxy_ConnectDenied(t *testing.T) {
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	// Allow only example.com
	proxy := NewProxy([]string{"example.com"})
	if err := proxy.Start(); err != nil {
		t.Fatalf("proxy start: %v", err)
	}
	defer proxy.Stop()

	client := proxyClient(proxy.Addr)
	resp, err := client.Get(backend.URL)
	if err != nil {
		// CONNECT tunnel blocked — this is expected
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 or error for denied CONNECT, got %d", resp.StatusCode)
	}
}

func TestProxy_WildcardMatching(t *testing.T) {
	p := NewProxy([]string{"*.example.com", "exact.host.com"})

	tests := []struct {
		host    string
		allowed bool
		desc    string
	}{
		{"sub.example.com", true, "wildcard match"},
		{"deep.sub.example.com", true, "deep wildcard match"},
		{"example.com", false, "wildcard root denied"},
		{"other.com", false, "mismatch"},
		{"exact.host.com", true, "exact match"},
		{"SUB.EXAMPLE.COM", true, "case insensitive"},
		{"notexample.com", false, "not a subdomain"},
	}

	for _, tc := range tests {
		got := p.domainAllowed(tc.host)
		if got != tc.allowed {
			t.Errorf("[%s] domainAllowed(%q) = %v, want %v", tc.desc, tc.host, got, tc.allowed)
		}
	}
}

func TestProxy_Stop(t *testing.T) {
	proxy := NewProxy([]string{"example.com"})
	if err := proxy.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	addr := proxy.Addr

	if err := proxy.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// After stop, the address should be unreachable
	conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Error("expected connection to fail after proxy stop")
	}
}

func TestProxy_MissingHost(t *testing.T) {
	proxy := NewProxy([]string{"example.com"})
	if err := proxy.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer proxy.Stop()

	// Send a request directly to the proxy without a Host header via a raw connection
	conn, err := net.Dial("tcp", proxy.Addr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()

	// Send a minimal HTTP request with no Host header
	req := "GET http:/// HTTP/1.0\r\n\r\n"
	_, err = fmt.Fprint(conn, req)
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 512)
	n, _ := conn.Read(buf)
	resp := string(buf[:n])

	if !strings.Contains(resp, "400") && !strings.Contains(resp, "403") {
		t.Errorf("expected 400 or 403 response for missing host, got: %s", resp)
	}
}
