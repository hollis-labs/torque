package sandbox

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Proxy is a domain-allowlisted HTTP proxy (supports plain HTTP and CONNECT tunneling).
type Proxy struct {
	AllowedDomains []string
	Addr           string
	listener       net.Listener
	server         *http.Server
	wg             sync.WaitGroup
}

// NewProxy creates a new Proxy with the given allowed domains.
func NewProxy(allowedDomains []string) *Proxy {
	return &Proxy{
		AllowedDomains: allowedDomains,
	}
}

// Start begins listening on a random port on 127.0.0.1 and serves requests.
func (p *Proxy) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("proxy listen: %w", err)
	}
	p.listener = ln
	p.Addr = ln.Addr().String()

	p.server = &http.Server{
		// Use the handler directly — http.ServeMux does not route CONNECT requests.
		Handler:      http.HandlerFunc(p.handleRequest),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		_ = p.server.Serve(ln)
	}()

	return nil
}

// Stop gracefully shuts down the proxy.
func (p *Proxy) Stop() error {
	if p.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := p.server.Shutdown(ctx)
	p.wg.Wait()
	return err
}

func (p *Proxy) handleRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleHTTP(w, r)
}

func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	host, _, err := splitHostPort(r.Host)
	if err != nil {
		http.Error(w, "bad host: "+err.Error(), http.StatusBadRequest)
		return
	}

	if !p.domainAllowed(host) {
		http.Error(w, "domain not allowed: "+host, http.StatusForbidden)
		return
	}

	// Dial the target
	targetConn, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
	if err != nil {
		http.Error(w, "dial error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer targetConn.Close()

	// Hijack the client connection
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "hijack error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	// Send 200 Connection Established
	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// Pipe both directions
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(targetConn, clientConn)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(clientConn, targetConn)
	}()
	wg.Wait()
}

func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Host == "" {
		http.Error(w, "missing host", http.StatusBadRequest)
		return
	}

	host, _, err := splitHostPort(r.Host)
	if err != nil {
		http.Error(w, "bad host: "+err.Error(), http.StatusBadRequest)
		return
	}

	if !p.domainAllowed(host) {
		http.Error(w, "domain not allowed: "+host, http.StatusForbidden)
		return
	}

	// Forward the request
	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	if outReq.URL.Scheme == "" {
		outReq.URL.Scheme = "http"
	}
	if outReq.URL.Host == "" {
		outReq.URL.Host = r.Host
	}

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 30 * time.Second,
	}

	resp, err := client.Do(outReq)
	if err != nil {
		http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// domainAllowed checks if host is in the allowed list.
// Supports exact matches and wildcard prefixes (*.example.com).
func (p *Proxy) domainAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, pattern := range p.AllowedDomains {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if strings.HasPrefix(pattern, "*.") {
			// Wildcard: *.example.com matches sub.example.com but NOT example.com
			suffix := pattern[1:] // ".example.com"
			if strings.HasSuffix(host, suffix) && host != suffix[1:] {
				return true
			}
		} else {
			if host == pattern {
				return true
			}
		}
	}
	return false
}

// splitHostPort splits host:port, handling the no-port case.
func splitHostPort(hostport string) (host, port string, err error) {
	// If it contains a colon that is not part of IPv6, attempt split
	host, port, err = net.SplitHostPort(hostport)
	if err != nil {
		// May be a plain hostname with no port
		if !strings.Contains(hostport, ":") {
			return hostport, "", nil
		}
		// Could be IPv6 without brackets; return as-is
		return hostport, "", nil
	}
	return host, port, nil
}
