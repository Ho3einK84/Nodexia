package servers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

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

	lastSeen := serverLastSeenMap(r.Context(), deps)
	items := make([]view.ServerSummary, 0, len(pageItems))
	for _, server := range pageItems {
		item := view.ServerSummary{
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
		}
		if ts, ok := lastSeen[server.ID]; ok {
			age := time.Since(ts)
			if age < 10*time.Minute {
				item.IsOnline = true
			} else {
				item.LastSeenAt = formatAge(age)
			}
		}
		items = append(items, item)
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

// serverLastSeenMap returns a map of server_id → last snapshot time by joining
// system_snapshots against a per-server MAX(id) subquery so that created_at is
// a regular column (not a GROUP-BY aggregate), which the SQLite driver returns
// as a typed time.Time rather than a raw string whose format may vary.
func serverLastSeenMap(ctx context.Context, deps module.Dependencies) map[int64]time.Time {
	if deps.Database == nil || deps.Database.SQL == nil {
		return nil
	}
	rows, err := deps.Database.SQL.QueryContext(ctx,
		`SELECT ss.server_id, ss.created_at
		 FROM system_snapshots ss
		 JOIN (
		   SELECT server_id, MAX(id) AS latest_id
		   FROM system_snapshots
		   GROUP BY server_id
		 ) latest ON latest.latest_id = ss.id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make(map[int64]time.Time)
	for rows.Next() {
		var serverID int64
		var raw any
		if err := rows.Scan(&serverID, &raw); err != nil {
			continue
		}
		if t := parseSnapshotTime(raw); !t.IsZero() {
			result[serverID] = t
		}
	}
	return result
}

// parseSnapshotTime decodes the flexible datetime values returned by the SQLite
// driver for the created_at column: time.Time, RFC3339(Nano), space-separated
// datetime with optional timezone, and plain "YYYY-MM-DD HH:MM:SS".
func parseSnapshotTime(value any) time.Time {
	switch v := value.(type) {
	case time.Time:
		return v.UTC()
	case string:
		return parseSnapshotTimeString(strings.TrimSpace(v))
	case []byte:
		return parseSnapshotTimeString(strings.TrimSpace(string(v)))
	default:
		return time.Time{}
	}
}

func parseSnapshotTimeString(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// formatAge converts a duration to a compact human-readable age string.
func formatAge(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	days := int(d.Hours() / 24)
	if days == 1 {
		return "1 day ago"
	}
	return fmt.Sprintf("%d days ago", days)
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
