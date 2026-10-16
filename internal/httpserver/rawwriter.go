package httpserver

import (
	"context"
	"net/http"
)

type rawWriterKey struct{}

// KeepRawWriter records the connection's own ResponseWriter on the request
// context. Put it OUTSIDE any middleware that wraps the writer: a wrapper that
// does not implement Unwrap (propagation.HTTPMiddleware's statusWriter, in
// libs core/util v0.4.0) hides the connection from http.ResponseController, so
// SetWriteDeadline would fail with "feature not supported". The SSE stream
// sets its write deadline on the raw writer instead; the deadline belongs to the
// connection, so it applies to writes made through any wrapper.
func KeepRawWriter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), rawWriterKey{}, w)))
	})
}

// connController returns the ResponseController for r's connection: the raw
// writer KeepRawWriter saved, else w itself.
func connController(w http.ResponseWriter, r *http.Request) *http.ResponseController {
	if raw, ok := r.Context().Value(rawWriterKey{}).(http.ResponseWriter); ok {
		return http.NewResponseController(raw)
	}
	return http.NewResponseController(w)
}
