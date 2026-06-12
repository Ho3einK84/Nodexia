package alerts

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

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

// buildEventsPagination assembles the windowed page control for the alert
// history section. Page links carry the #alert-history fragment so a click
// lands back at the history card instead of the top of the page.
func buildEventsPagination(currentPage, totalPages int) view.PaginationView {
	makeURL := func(page int) string {
		if page <= 1 {
			return "/alerts#alert-history"
		}
		return "/alerts?events_page=" + strconv.Itoa(page) + "#alert-history"
	}

	pages := make([]view.PaginationPageView, 0, totalPages)
	for _, number := range eventsPageWindow(currentPage, totalPages) {
		if number == 0 {
			pages = append(pages, view.PaginationPageView{IsGap: true})
			continue
		}
		pages = append(pages, view.PaginationPageView{
			Number:   number,
			URL:      makeURL(number),
			IsActive: number == currentPage,
		})
	}

	return view.PaginationView{
		CurrentPage: currentPage,
		TotalPages:  totalPages,
		HasPrev:     currentPage > 1,
		HasNext:     currentPage < totalPages,
		PrevURL:     makeURL(currentPage - 1),
		NextURL:     makeURL(currentPage + 1),
		Pages:       pages,
	}
}

// eventsPageWindow returns the page numbers to render, using 0 as a gap
// (ellipsis) marker. Up to 7 pages render in full; beyond that it windows
// around current with first/last anchors. Mirrors the servers pagination.
func eventsPageWindow(current, total int) []int {
	if total <= 7 {
		nums := make([]int, 0, total)
		for i := 1; i <= total; i++ {
			nums = append(nums, i)
		}
		return nums
	}

	start, end := current-1, current+1
	if start < 2 {
		start = 2
	}
	if end > total-1 {
		end = total - 1
	}

	nums := []int{1}
	if start > 2 {
		nums = append(nums, 0)
	}
	for i := start; i <= end; i++ {
		nums = append(nums, i)
	}
	if end < total-1 {
		nums = append(nums, 0)
	}
	return append(nums, total)
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
