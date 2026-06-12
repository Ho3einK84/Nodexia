package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestLoggingRecorderSupportsHijack guards the WebSocket upgrade path: the
// logging wrapper must expose http.Hijacker, otherwise every terminal
// connection fails during the handshake (the recorder used to swallow it).
func TestLoggingRecorderSupportsHijack(t *testing.T) {
	hijacked := make(chan error, 1)

	handler := Logging()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			hijacked <- io.EOF
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			hijacked <- err
			return
		}
		// The handler owns the connection now; respond raw and close.
		_, _ = conn.Write([]byte("HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n"))
		_ = conn.Close()
		hijacked <- nil
	}))

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("GET error = %v", err)
	}
	resp.Body.Close()

	if err := <-hijacked; err != nil {
		if err == io.EOF {
			t.Fatal("logging recorder does not implement http.Hijacker")
		}
		t.Fatalf("Hijack() error = %v", err)
	}
}

// TestLoggingRecorderSupportsFlush keeps streaming handlers working through
// the logging wrapper.
func TestLoggingRecorderSupportsFlush(t *testing.T) {
	handler := Logging()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("GET error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (flusher missing on recorder)", resp.StatusCode)
	}
}
