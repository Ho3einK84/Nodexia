package middleware

import (
	"bufio"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// Flush forwards streaming writes when the underlying writer supports them.
func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack exposes the underlying connection so WebSocket upgrades work through
// the logging wrapper (without it, coder/websocket's Accept fails and every
// terminal connection dies during the handshake).
//
// net/http arms the connection with the server's ReadTimeout/WriteTimeout
// deadlines before the handler runs, and a hijack does NOT clear them — a
// long-lived WebSocket would be killed as soon as those deadlines elapse. The
// hijacked connection belongs to the handler, so clear the deadlines here;
// the WebSocket library applies its own per-frame deadlines.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("middleware: underlying ResponseWriter does not implement http.Hijacker")
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, nil, err
	}
	r.statusCode = http.StatusSwitchingProtocols
	_ = conn.SetDeadline(time.Time{})
	return conn, rw, nil
}

func Logging() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startedAt := time.Now()
			recorder := &statusRecorder{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(recorder, r)

			level := slog.LevelInfo
			if recorder.statusCode >= http.StatusInternalServerError {
				level = slog.LevelError
			} else if recorder.statusCode >= http.StatusBadRequest {
				level = slog.LevelWarn
			}

			slog.Log(r.Context(), level, "http request",
				slog.String("method", safeLogValue(r.Method)),
				slog.String("path", safeLogValue(r.URL.Path)),
				slog.Int("status", recorder.statusCode),
				slog.Duration("duration", time.Since(startedAt).Round(time.Millisecond)),
				slog.String("request_id", GetRequestID(r.Context())),
				slog.String("remote_addr", safeLogValue(remoteAddress(r.RemoteAddr))),
			)
		})
	}
}

func safeLogValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	runes := []rune(value)
	if len(runes) > 240 {
		return string(runes[:240]) + "..."
	}
	return value
}

func remoteAddress(value string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(value))
	if err == nil {
		return host
	}
	return strings.TrimSpace(value)
}
