package alerts

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

func newAlertsPage(deps module.Dependencies) view.PageData {
	page := view.NewPageData(deps.Config)
	page.Title = "Alerts"
	page.ActiveNav = "/alerts"
	if deps.Database != nil {
		page.MigrationCount = deps.Database.MigrationCount()
	}
	return page
}

func renderOverview(
	w http.ResponseWriter,
	r *http.Request,
	deps module.Dependencies,
	statusCode int,
	overview view.AlertsOverviewView,
	flashKind, flashMessage string,
) {
	page := newAlertsPage(deps)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.ContentTemplate = "content-alerts-overview"
	page.PageTitle = "Alerting"
	page.PageDescription = "Define metric thresholds, route notifications to Telegram channels, and mute servers that are intentionally noisy."
	page.AlertsOverview = overview
	page.FlashKind = flashKind
	page.FlashMessage = flashMessage

	if err := deps.Renderer.Render(w, statusCode, page); err != nil {
		http.Error(w, "render alerts page", http.StatusInternalServerError)
	}
}

func renderRuleForm(
	w http.ResponseWriter,
	r *http.Request,
	deps module.Dependencies,
	statusCode int,
	pageTitle, pageDescription string,
	form view.AlertRuleFormView,
	flashKind, flashMessage string,
) {
	page := newAlertsPage(deps)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.ContentTemplate = "content-alert-rule-form"
	page.PageTitle = pageTitle
	page.PageDescription = pageDescription
	page.IsEditingAlertRule = form.ID > 0
	page.AlertRuleForm = form
	page.FlashKind = flashKind
	page.FlashMessage = flashMessage

	if err := deps.Renderer.Render(w, statusCode, page); err != nil {
		http.Error(w, "render alert rule form", http.StatusInternalServerError)
	}
}

func renderChannelForm(
	w http.ResponseWriter,
	r *http.Request,
	deps module.Dependencies,
	statusCode int,
	pageTitle, pageDescription string,
	form view.AlertChannelFormView,
	flashKind, flashMessage string,
) {
	page := newAlertsPage(deps)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.ContentTemplate = "content-alert-channel-form"
	page.PageTitle = pageTitle
	page.PageDescription = pageDescription
	page.IsEditingAlertChannel = form.ID > 0
	page.AlertChannelForm = form
	page.FlashKind = flashKind
	page.FlashMessage = flashMessage

	if err := deps.Renderer.Render(w, statusCode, page); err != nil {
		http.Error(w, "render alert channel form", http.StatusInternalServerError)
	}
}

// renderError renders an SSR error page, mapping ErrNotFound to a 404.
func renderError(w http.ResponseWriter, deps module.Dependencies, err error, fallbackTitle, fallbackMessage string) {
	statusCode := http.StatusInternalServerError
	title := fallbackTitle
	message := fallbackMessage
	if errors.Is(err, ErrNotFound) {
		statusCode = http.StatusNotFound
		title = "Not found"
		message = "The requested alert record does not exist anymore."
	}

	slog.Warn("alerts request failed",
		slog.Int("status", statusCode),
		slog.String("title", title),
		slog.String("error", err.Error()),
	)

	page := view.NewErrorPageData(deps.Config, statusCode, title, message)
	page.ActiveNav = "/alerts"
	if renderErr := deps.Renderer.Render(w, statusCode, page); renderErr != nil {
		http.Error(w, fallbackMessage, statusCode)
	}
}
