package servers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/geoip"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

func newServersPage(deps module.Dependencies, r *http.Request) view.PageData {
	page := view.NewPageData(deps.Config, r)
	page.Title = page.T("servers.title")
	page.ActiveNav = "/servers"
	page.MigrationCount = deps.Database.MigrationCount()
	return page
}

// serversPerPage caps how many server cards render on a single list page.
const serversPerPage = 10

func renderListPage(w http.ResponseWriter, r *http.Request, deps module.Dependencies, servers []Server, query, tagFilter string, page int, flashKind, flashMessage string) {
	totalRegistered := len(servers)

	// Tag chips are built from the FULL registry (before filtering) so every
	// tag stays reachable while one is active.
	tagOptions := buildTagOptions(servers, query, tagFilter)

	matched := filterByTag(filterServers(servers, query), tagFilter)
	pageItems, currentPage, totalPages := paginateServers(matched, page, serversPerPage)

	telemetry := serverTelemetryMap(r.Context(), deps)
	nodeCounts := serverNodeCountsMap(r.Context(), deps)

	// A server is "up" while its last snapshot is within ~2 monitoring intervals
	// (tolerates one missed sweep); older than that and it is treated as "down".
	// "Just now" covers a freshly collected snapshot.
	upWindow := deps.Config.Scheduler.MonitoringInterval * 2
	if upWindow < 10*time.Minute {
		upWindow = 10 * time.Minute
	}
	const justNowWindow = 2 * time.Minute

	upCount, downCount := 0, 0
	for _, server := range servers {
		if tel, ok := telemetry[server.ID]; ok {
			if time.Since(tel.lastSeen) <= upWindow {
				upCount++
			} else {
				downCount++
			}
		}
	}

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
			CreatedAt:          formatTimestamp(server.CreatedAt),
			UpdatedAt:          formatTimestamp(server.UpdatedAt),
			FlagEmoji:          geoip.FlagEmoji(server.CountryCode),
			CountryCode:        server.CountryCode,
			CountryName:        server.CountryName,
		}
		if tel, ok := telemetry[server.ID]; ok {
			age := time.Since(tel.lastSeen)
			switch {
			case age <= upWindow:
				item.Status = "up"
			default:
				item.Status = "down"
			}
			if age < justNowWindow {
				item.JustNow = true
			} else {
				item.LastSeenAt = formatAge(age)
			}
			item.HasTelemetry = true
			item.CPUPercent = clampPercent(tel.cpu)
			item.RAMPercent = clampPercent(tel.ram)
			item.DiskPercent = clampPercent(tel.disk)
			item.CPULevel = usageLevel(item.CPUPercent)
			item.RAMLevel = usageLevel(item.RAMPercent)
			item.DiskLevel = usageLevel(item.DiskPercent)
		} else {
			item.Status = "unknown"
		}
		if nc, ok := nodeCounts[server.ID]; ok {
			item.NodeCount = nc.total
			item.RunningNodeCount = nc.running
			item.StoppedNodeCount = nc.stopped
		}
		items = append(items, item)
	}

	showingFrom, showingTo := 0, 0
	if len(items) > 0 {
		showingFrom = (currentPage-1)*serversPerPage + 1
		showingTo = showingFrom + len(items) - 1
	}

	totalNodes := 0
	for _, nc := range nodeCounts {
		totalNodes += nc.total
	}

	pd := newServersPage(deps, r)
	pd.CSRFToken = middleware.GetCSRFToken(r.Context())
	pd.ContentTemplate = "content-servers-list"
	pd.PageTitle = pd.T("servers.list_title")
	pd.PageDescription = pd.T("servers.list_description")
	pd.ServerCount = totalRegistered
	pd.ServersUpCount = upCount
	pd.ServersDownCount = downCount
	pd.TotalNodeCount = totalNodes
	pd.Servers = items
	pd.ServerSearch = query
	pd.ServerTagFilter = tagFilter
	pd.ServerTagOptions = tagOptions
	pd.ServerMatchCount = len(matched)
	pd.ServerShowingFrom = showingFrom
	pd.ServerShowingTo = showingTo
	pd.ServerPagination = buildPagination(currentPage, totalPages, query, tagFilter)
	defaultForm := NewFormViewData(DefaultFormInput(), ValidationErrors{})
	pd.ServerFormAction = defaultForm.Action
	pd.ServerAddModalOpen = strings.TrimSpace(r.URL.Query().Get("add")) == "1"
	pd.ServerForm = view.ServerFormView{
		Port:               defaultForm.Port,
		AuthMode:           defaultForm.AuthMode,
		CredentialStrategy: defaultForm.CredentialStrategy,
		TrafficResetDay:    defaultForm.TrafficResetDay,
		Errors:             defaultForm.Errors,
	}
	pd.FlashKind = flashKind
	pd.FlashMessage = flashMessage
	pd.PageStyles = []string{"/static/servers.css", "/static/bulk.css"}
	pd.PageScripts = []string{"/static/bulk.js"}

	if err := deps.Renderer.Render(w, http.StatusOK, pd); err != nil {
		http.Error(w, "render servers page", http.StatusInternalServerError)
	}
}

// filterByTag keeps only servers carrying the exact tag (tags are stored
// lowercase). An empty tag is a no-op.
func filterByTag(servers []Server, tag string) []Server {
	if tag == "" {
		return servers
	}
	filtered := make([]Server, 0, len(servers))
	for _, server := range servers {
		for _, candidate := range server.Tags {
			if candidate == tag {
				filtered = append(filtered, server)
				break
			}
		}
	}
	return filtered
}

// buildTagOptions folds every distinct tag in the registry into a chip row:
// each chip toggles its tag filter (preserving the search query), and the
// active chip links back to the unfiltered list. Sorted alphabetically.
func buildTagOptions(servers []Server, query, activeTag string) []view.ServerTagOption {
	counts := map[string]int{}
	for _, server := range servers {
		for _, tag := range server.Tags {
			counts[tag]++
		}
	}
	tags := make([]string, 0, len(counts))
	for tag := range counts {
		tags = append(tags, tag)
	}
	sort.Strings(tags)

	out := make([]view.ServerTagOption, 0, len(tags))
	for _, tag := range tags {
		target := ""
		if tag != activeTag {
			target = tag
		}
		values := url.Values{}
		if query != "" {
			values.Set("q", query)
		}
		if target != "" {
			values.Set("tag", target)
		}
		href := "/servers"
		if encoded := values.Encode(); encoded != "" {
			href += "?" + encoded
		}
		out = append(out, view.ServerTagOption{
			Tag:    tag,
			Count:  counts[tag],
			URL:    href,
			Active: tag == activeTag,
		})
	}
	return out
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
		server.CountryCode,
		server.CountryName,
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
// search query and tag filter in every link.
func buildPagination(currentPage, totalPages int, query, tagFilter string) view.PaginationView {
	makeURL := func(page int) string {
		values := url.Values{}
		if query != "" {
			values.Set("q", query)
		}
		if tagFilter != "" {
			values.Set("tag", tagFilter)
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

type serverTelemetry struct {
	lastSeen time.Time
	cpu      float64
	ram      float64
	disk     float64
}

type serverNodeCounts struct {
	total   int
	running int
	stopped int
}

// serverTelemetryMap returns a map of server_id → latest snapshot telemetry
// (timestamp + CPU/RAM/Disk percentages) by joining system_snapshots against
// a per-server MAX(id) subquery so that created_at is a regular column rather
// than a GROUP-BY aggregate.
func serverTelemetryMap(ctx context.Context, deps module.Dependencies) map[int64]serverTelemetry {
	readDB := deps.Database.Reader()
	if readDB == nil {
		return nil
	}
	rows, err := readDB.QueryContext(ctx,
		`SELECT ss.server_id, ss.cpu_usage, ss.ram_usage, ss.disk_usage, ss.created_at
		 FROM system_snapshots ss
		 JOIN (
		   SELECT server_id, MAX(id) AS latest_id
		   FROM system_snapshots
		   GROUP BY server_id
		 ) latest ON latest.latest_id = ss.id`)
	if err != nil {
		return nil
	}
	defer func() {
		_ = rows.Close()
	}()
	result := make(map[int64]serverTelemetry)
	for rows.Next() {
		var serverID int64
		var cpu, ram, disk float64
		var raw any
		if err := rows.Scan(&serverID, &cpu, &ram, &disk, &raw); err != nil {
			continue
		}
		if t := parseSnapshotTime(raw); !t.IsZero() {
			result[serverID] = serverTelemetry{
				lastSeen: t,
				cpu:      cpu,
				ram:      ram,
				disk:     disk,
			}
		}
	}
	return result
}

func clampPercent(value float64) int {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return int(math.Round(value))
}

func usageLevel(pct int) string {
	switch {
	case pct > 85:
		return "crit"
	case pct > 60:
		return "warn"
	default:
		return "ok"
	}
}

// serverNodeCountsMap collects per-server total/running/stopped node tallies
// from each server's latest discovery sweep (excluding the "none" sentinel).
func serverNodeCountsMap(ctx context.Context, deps module.Dependencies) map[int64]serverNodeCounts {
	readDB := deps.Database.Reader()
	if readDB == nil {
		return nil
	}
	rows, err := readDB.QueryContext(ctx,
		`SELECT ns.server_id, ns.health_status
		 FROM node_snapshots ns
		 WHERE ns.node_type <> 'none'
		   AND ns.created_at = (
		     SELECT created_at FROM node_snapshots
		     WHERE server_id = ns.server_id
		     ORDER BY id DESC
		     LIMIT 1
		   )`)
	if err != nil {
		return nil
	}
	defer func() {
		_ = rows.Close()
	}()
	result := make(map[int64]serverNodeCounts)
	for rows.Next() {
		var serverID int64
		var health string
		if err := rows.Scan(&serverID, &health); err != nil {
			continue
		}
		entry := result[serverID]
		entry.total++
		switch strings.ToLower(strings.TrimSpace(health)) {
		case "running", "active", "healthy":
			entry.running++
		default:
			entry.stopped++
		}
		result[serverID] = entry
	}
	return result
}

// totalNodeCount sums the discovered node instances across every server's latest
// discovery sweep. A sweep that found nothing persists a single "none" sentinel
// row (see nodes.ReplaceLatest), so those are excluded. The per-server "latest
// sweep" is the rows sharing the created_at of that server's highest-id row —
// the exact definition nodes.GetLatestByServer uses — so this count always
// matches what the nodes pages show. Returns 0 on any error or when no discovery
// has run yet (portable SQL: correlated subquery, no driver-specific syntax).
func totalNodeCount(ctx context.Context, deps module.Dependencies) int {
	if deps.Database == nil || deps.Database.SQL == nil {
		return 0
	}
	var count int
	err := deps.Database.SQL.QueryRowContext(ctx,
		`SELECT COUNT(*)
		 FROM node_snapshots ns
		 WHERE ns.node_type <> 'none'
		   AND ns.created_at = (
		     SELECT created_at FROM node_snapshots
		     WHERE server_id = ns.server_id
		     ORDER BY id DESC
		     LIMIT 1
		   )`).Scan(&count)
	if err != nil {
		return 0
	}
	return count
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
	page := newServersPage(deps, r)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.ContentTemplate = "content-server-form"
	// pageTitle/pageDescription/flashMessage are translation keys resolved here
	// in the request's active language.
	page.PageTitle = page.T(pageTitle)
	page.PageDescription = page.T(pageDescription)
	page.IsEditingServer = form.ID > 0
	page.ServerFormAction = form.Action
	page.ServerDeleteAction = form.DeleteAction
	page.ServerForm = view.ServerFormView{
		ID:                  form.ID,
		Name:                form.Name,
		Host:                form.Host,
		Port:                form.Port,
		AuthMode:            form.AuthMode,
		Username:            form.Username,
		Tags:                form.Tags,
		Note:                form.Note,
		CredentialStrategy:  form.CredentialStrategy,
		CredentialRef:       form.CredentialRef,
		HasStoredCredential: form.HasStoredCredential,
		TrafficResetDay:     form.TrafficResetDay,
		Errors:              form.Errors,
	}
	page.ServerTagOptions = loadFleetTagOptions(r.Context(), deps)
	page.FlashKind = flashKind
	page.FlashMessage = page.T(flashMessage)
	page.PageStyles = []string{"/static/servers.css"}

	if err := deps.Renderer.Render(w, statusCode, page); err != nil {
		http.Error(w, "render server form page", http.StatusInternalServerError)
	}
}

func loadFleetTagOptions(ctx context.Context, deps module.Dependencies) []view.ServerTagOption {
	if deps.Database == nil || deps.Database.SQL == nil {
		return nil
	}
	rows, err := deps.Database.SQL.QueryContext(ctx,
		`SELECT tag, COUNT(*) FROM server_tags GROUP BY tag ORDER BY tag ASC`)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []view.ServerTagOption
	for rows.Next() {
		var tag string
		var count int
		if err := rows.Scan(&tag, &count); err != nil || strings.TrimSpace(tag) == "" {
			continue
		}
		out = append(out, view.ServerTagOption{Tag: tag, Count: count})
	}
	return out
}

// renderRepositoryError renders an SSR error page. fallbackTitle/fallbackMessage
// are translation keys resolved in the request's active language.
func renderRepositoryError(w http.ResponseWriter, r *http.Request, deps module.Dependencies, err error, fallbackTitle string, fallbackMessage string) {
	statusCode := http.StatusInternalServerError
	titleKey := fallbackTitle
	messageKey := fallbackMessage
	if errors.Is(err, ErrNotFound) {
		statusCode = http.StatusNotFound
		titleKey = "servers.error.not_found_title"
		messageKey = "servers.error.not_found_message"
	}

	loc := localizerFromRequest(r)
	title := loc.T(titleKey)
	message := loc.T(messageKey)

	slog.Warn("server lookup failed",
		slog.Int("status", statusCode),
		slog.String("title", title),
		slog.String("error", err.Error()),
	)

	page := view.NewErrorPageData(deps.Config, r, statusCode, title, message)
	page.ActiveNav = "/servers"
	if renderErr := deps.Renderer.Render(w, statusCode, page); renderErr != nil {
		http.Error(w, fallbackMessage, statusCode)
	}
}
