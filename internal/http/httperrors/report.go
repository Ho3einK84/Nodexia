package httperrors

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

// RenderPage logs the failure with request context and renders an SSR error page.
func RenderPage(w http.ResponseWriter, r *http.Request, deps module.Dependencies, err error, activeNav, fallbackTitle, fallbackMessage string) {
	statusCode := http.StatusInternalServerError
	title := fallbackTitle
	message := fallbackMessage

	if err != nil && errors.Is(err, servers.ErrNotFound) {
		statusCode = http.StatusNotFound
		title = "Server not found"
		message = "The requested server record does not exist anymore."
	}

	requestID := middleware.GetRequestID(r.Context())
	logFailure(r, requestID, statusCode, title, err)

	page := view.NewErrorPageData(deps.Config, statusCode, title, message)
	page.ActiveNav = activeNav
	page.RequestID = requestID
	page.ErrorDetail = operatorDetail(deps.Config.Environment, requestID, err)

	if renderErr := deps.Renderer.Render(w, statusCode, page); renderErr != nil {
		slog.Error("render error page failed",
			slog.String("request_id", requestID),
			slog.String("render_error", renderErr.Error()),
		)
		http.Error(w, fallbackMessage, statusCode)
	}
}

func logFailure(r *http.Request, requestID string, statusCode int, title string, err error) {
	attrs := []any{
		slog.String("request_id", requestID),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Int("status", statusCode),
		slog.String("title", title),
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
		slog.Error("request failed", attrs...)
		return
	}
	slog.Warn("request failed", attrs...)
}

func operatorDetail(environment, requestID string, err error) string {
	if requestID != "" {
		if environment == "production" || environment == "staging" {
			return "Reference request_id=" + requestID + " in the server logs for details."
		}
	}
	if err != nil {
		return err.Error()
	}
	return ""
}
