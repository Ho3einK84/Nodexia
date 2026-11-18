// Package sse provides a tiny Server-Sent Events writer used by the live
// command / node / bulk streams. It replaces the old 2-second full-page poll:
// handlers push output chunks down a single long-lived HTTP response while the
// browser consumes them through an EventSource.
package sse

import (
	"net/http"
	"strings"
	"time"
)

// Writer wraps an http.ResponseWriter for streaming SSE frames. It sets the
// SSE headers, clears the server write deadline (these connections outlive the
// default WriteTimeout), and flushes after every frame so chunks arrive live.
type Writer struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

// NewWriter prepares w for event-stream output. The X-Accel-Buffering header
// disables response buffering on nginx/Caddy so frames are not held back.
func NewWriter(w http.ResponseWriter) *Writer {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	// Long-lived stream: clear the server's WriteTimeout (default 60s) so it is
	// not torn down mid-stream. Unwrap() on the logging recorder lets the
	// controller reach the underlying connection.
	_ = rc.SetWriteDeadline(time.Time{})

	w.WriteHeader(http.StatusOK)
	_ = rc.Flush()
	return &Writer{w: w, rc: rc}
}

// Event writes one SSE event. Multi-line data is split into multiple `data:`
// lines per the spec; the browser rejoins them with "\n", so a streamed chunk
// is reconstructed verbatim on the client.
func (x *Writer) Event(event, data string) error {
	var b strings.Builder
	if event != "" {
		b.WriteString("event: ")
		b.WriteString(event)
		b.WriteByte('\n')
	}
	data = strings.ReplaceAll(data, "\r\n", "\n")
	for _, line := range strings.Split(data, "\n") {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')

	if _, err := x.w.Write([]byte(b.String())); err != nil {
		return err
	}
	return x.rc.Flush()
}

// Comment writes an SSE comment line (": text"). Used as a keep-alive so idle
// proxies do not drop a stream that is quietly waiting for more output.
func (x *Writer) Comment(text string) error {
	if _, err := x.w.Write([]byte(": " + text + "\n\n")); err != nil {
		return err
	}
	return x.rc.Flush()
}
