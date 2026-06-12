package servers

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

func newServersPage(deps module.Dependencies) view.PageData {
	page := view.NewPageData(deps.Config)
	page.Title = "Servers"
	page.ActiveNav = "/servers"
	page.MigrationCount = deps.Database.MigrationCount()
	return page
}

// serversPerPage caps how many server cards render on a single list page.
const serversPerPage = 10

func renderListPage(w http.ResponseWriter, r *http.Request, deps module.Dependencies, servers []Server, query string, page int, flashKind, flashMessage string) {
	totalRegistered := len(servers)

	matched := filterServers(servers, query)
	pageItems, currentPage, totalPages := paginateServers(matched, page, serversPerPage)

	items := make([]view.ServerSummary, 0, len(pageItems))
	for _, server := range pageItems {
		items = append(items, view.ServerSummary{
			ID:                 server.ID,
			Name:               server.Name,
			Host:               server.Host,
			Port:               server.Port,
			AuthMode:           server.AuthMode,
			Username:           server.Username,
			Note:               server.Note,
			Tags:               server.Tags,
			CredentialStrategy: server.CredentialStrategy,
			CredentialRef:      server.CredentialRef,
			CreatedAt:          formatTimestamp(server.CreatedAt),
			UpdatedAt:          formatTimestamp(server.UpdatedAt),
		})
	}

	showingFrom, showingTo := 0, 0
	if len(items) > 0 {
		showingFrom = (currentPage-1)*serversPerPage + 1
		showingTo = showingFrom + len(items) - 1
	}

	pd := newServersPage(deps)
	pd.CSRFToken = middleware.GetCSRFToken(r.Context())
	pd.ContentTemplate = "content-servers-list"
	pd.PageTitle = "Server registry"
	pd.PageDescription = "Your managed Rebecca and PasarGuard servers. Register a target to start collecting monitoring and node data."
	pd.ServerCount = totalRegistered
	pd.Servers = items
	pd.ServerSearch = query
	pd.ServerMatchCount = len(matched)
	pd.ServerShowingFrom = showingFrom
	pd.ServerShowingTo = showingTo
	pd.ServerPagination = buildPagination(currentPage, totalPages, query)
	pd.FlashKind = flashKind
	pd.FlashMessage = flashMessage
	pd.PageStyles = []string{"/static/bulk.css"}
	pd.PageScripts = []string{"/static/bulk.js"}

	if err := deps.Renderer.Render(w, http.StatusOK, pd); err != nil {
		http.Error(w, "render servers page", http.StatusInternalServerError)
	}
}

// filterServers returns servers whose name, host, username, note, auth mode,
// tags, or port contain the (case-insensitive) query. An empty query is a no-op.
func filterServers(servers []Server, query string) []Server {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return servers
	}
	filtered := make([]Server, 0, len(servers))
	for _, server := range servers {
		if serverMatchesQuery(server, query) {
			filtered = append(filtered, server)
		}
	}
	return filtered
}

func serverMatchesQuery(server Server, query string) bool {
	haystacks := []string{
		server.Name,
		server.Host,
		server.Username,
		server.Note,
		server.AuthMode,
		server.CredentialStrategy,
		strconv.Itoa(server.Port),
	}
	for _, field := range haystacks {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	for _, tag := range server.Tags {
		if strings.Contains(strings.ToLower(tag), query) {
			return true
		}
	}
	return false
}

// paginateServers clamps page into [1, totalPages] and returns the slice for
// that page. totalPages is at least 1 even when there are no results.
func paginateServers(servers []Server, page, perPage int) (items []Server, currentPage, totalPages int) {
	total := len(servers)
	totalPages = (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}

	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	return servers[start:end], page, totalPages
}

// buildPagination assembles the windowed page control, preserving the active
// search query in every link.
func buildPagination(currentPage, totalPages int, query string) view.PaginationView {
	makeURL := func(page int) string {
		values := url.Values{}
		if query != "" {
			values.Set("q", query)
		}
		if page > 1 {
			values.Set("page", strconv.Itoa(page))
		}
		if encoded := values.Encode(); encoded != "" {
			return "/servers?" + encoded
		}
		return "/servers"
	}

	pages := make([]view.PaginationPageView, 0, totalPages)
	for _, number := range pageWindow(currentPage, totalPages) {
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

// pageWindow returns the page numbers to render, using 0 as a gap (ellipsis)
// marker. Up to 7 pages render in full; beyond that it windows around current
// with first/last anchors.
func pageWindow(current, total int) []int {
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

func renderFormPage(
	w http.ResponseWriter,
	r *http.Request,
	deps module.Dependencies,
	statusCode int,
	pageTitle string,
	pageDescription string,
	form ServerFormViewData,
	flashKind string,
	flashMessage string,
) {
	page := newServersPage(deps)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.ContentTemplate = "content-server-form"
	page.PageTitle = pageTitle
	page.PageDescription = pageDescription
	page.IsEditingServer = form.ID > 0
	page.ServerFormAction = form.Action
	page.ServerDeleteAction = form.DeleteAction
	page.ServerForm = view.ServerFormView{
		ID:                 form.ID,
		Name:               form.Name,
		Host:               form.Host,
		Port:               form.Port,
		AuthMode:           form.AuthMode,
		Username:           form.Username,
		Tags:               form.Tags,
		Note:               form.Note,
		CredentialStrategy: form.CredentialStrategy,
		CredentialRef:      form.CredentialRef,
		Errors:             form.Errors,
	}
	page.FlashKind = flashKind
	page.FlashMessage = flashMessage

	if err := deps.Renderer.Render(w, statusCode, page); err != nil {
		http.Error(w, "render server form page", http.StatusInternalServerError)
	}
}

func renderRepositoryError(w http.ResponseWriter, deps module.Dependencies, err error, fallbackTitle string, fallbackMessage string) {
	statusCode := http.StatusInternalServerError
	title := fallbackTitle
	message := fallbackMessage
	if errors.Is(err, ErrNotFound) {
		statusCode = http.StatusNotFound
		title = "Server not found"
		message = "The requested server record does not exist anymore."
	}

	slog.Warn("server lookup failed",
		slog.Int("status", statusCode),
		slog.String("title", title),
		slog.String("error", err.Error()),
	)

	page := view.NewErrorPageData(deps.Config, statusCode, title, message)
	page.ActiveNav = "/servers"
	if renderErr := deps.Renderer.Render(w, statusCode, page); renderErr != nil {
		http.Error(w, fallbackMessage, statusCode)
	}
}
