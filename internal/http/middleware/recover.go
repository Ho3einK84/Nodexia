package middleware

import (
	"log/slog"
	"net/http"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

func Recover(cfg config.Config, renderer *view.Renderer) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					requestID := GetRequestID(r.Context())
					slog.Error("http panic recovered",
						slog.String("request_id", requestID),
						slog.String("method", safeLogValue(r.Method)),
						slog.String("path", safeLogValue(r.URL.Path)),
						slog.String("panic", safeLogValue(toPanicMessage(recovered))),
					)

					page := view.NewErrorPageData(
						cfg,
						http.StatusInternalServerError,
						"Internal server error",
						"The request failed unexpectedly while the server was processing it.",
					)
					page.RequestID = requestID
					page.ErrorDetail = "Reference request_id=" + requestID + " in the server logs for details."
					if err := renderer.Render(w, http.StatusInternalServerError, page); err != nil {
						http.Error(w, "internal server error", http.StatusInternalServerError)
					}
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

func toPanicMessage(value any) string {
	switch typed := value.(type) {
	case error:
		return typed.Error()
	case string:
		return typed
	default:
		return "panic"
	}
}
