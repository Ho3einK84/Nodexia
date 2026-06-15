package alerts

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/i18n"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

// localizerFromRequest returns the request's active localizer, falling back to
// the default-language localizer so error/flash helpers never panic.
func localizerFromRequest(r *http.Request) *i18n.Localizer {
	if loc := i18n.FromContext(r.Context()); loc != nil {
		return loc
	}
	return i18n.MustDefault().Localizer(i18n.DefaultLanguage)
}

func newAlertsPage(deps module.Dependencies, r *http.Request) view.PageData {
	page := view.NewPageData(deps.Config, r)
	page.Title = page.T("nav.alerts")
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
	page := newAlertsPage(deps, r)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.ContentTemplate = "content-alerts-overview"
	page.PageTitle = page.T("alerts.overview_title")
	page.PageDescription = page.T("alerts.overview_description")
	page.AlertsOverview = overview
	page.FlashKind = flashKind
	page.FlashMessage = page.T(flashMessage)

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
	page := newAlertsPage(deps, r)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.ContentTemplate = "content-alert-rule-form"
	page.PageTitle = page.T(pageTitle)
	page.PageDescription = page.T(pageDescription)
	page.IsEditingAlertRule = form.ID > 0
	page.AlertRuleForm = form
	page.FlashKind = flashKind
	page.FlashMessage = page.T(flashMessage)

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
	page := newAlertsPage(deps, r)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.ContentTemplate = "content-alert-channel-form"
	page.PageTitle = page.T(pageTitle)
	page.PageDescription = page.T(pageDescription)
	page.IsEditingAlertChannel = form.ID > 0
	page.AlertChannelForm = form
	page.FlashKind = flashKind
	page.FlashMessage = page.T(flashMessage)

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
// fallbackTitle/fallbackMessage are translation keys resolved in the request's
// active language.
func renderError(w http.ResponseWriter, r *http.Request, deps module.Dependencies, err error, fallbackTitle, fallbackMessage string) {
	statusCode := http.StatusInternalServerError
	titleKey := fallbackTitle
	messageKey := fallbackMessage
	if errors.Is(err, ErrNotFound) {
		statusCode = http.StatusNotFound
		titleKey = "alerts.error.not_found_title"
		messageKey = "alerts.error.not_found_message"
	}

	loc := localizerFromRequest(r)
	title := loc.T(titleKey)
	message := loc.T(messageKey)

	slog.Warn("alerts request failed",
		slog.Int("status", statusCode),
		slog.String("title", title),
		slog.String("error", err.Error()),
	)

	page := view.NewErrorPageData(deps.Config, r, statusCode, title, message)
	page.ActiveNav = "/alerts"
	if renderErr := deps.Renderer.Render(w, statusCode, page); renderErr != nil {
		http.Error(w, fallbackMessage, statusCode)
	}
}
