package middleware

import (
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
	if len(value) > 240 {
		return value[:240] + "..."
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
