package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/db"
	"github.com/Ho3einK84/Nodexia/internal/geoip"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/module/monitoring"
	"github.com/Ho3einK84/Nodexia/internal/module/nodes"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/scheduler"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

// countryBadge is a resolved flag emoji + country name for one server, used to
// decorate the home dashboard's server listings.
type countryBadge struct {
	Flag string
	Name string
}

type HomeHandler struct {
	config      config.Config
	database    *db.Runtime
	renderer    *view.Renderer
	scheduler   *scheduler.Runtime
	routeGroups []string
}

func NewHomeHandler(cfg config.Config, database *db.Runtime, renderer *view.Renderer, backgroundScheduler *scheduler.Runtime, routeGroups []string) HomeHandler {
	return HomeHandler{
		config:      cfg,
		database:    database,
		renderer:    renderer,
		scheduler:   backgroundScheduler,
		routeGroups: routeGroups,
	}
}

const homeDashboardPerPage = 5

func (h HomeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	snapPage, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("snap_page")))
	schedPage, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("sched_page")))

	page := view.NewPageData(h.config, r)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = "Nodexia"
	page.ContentTemplate = "content-home"
	page.ActiveNav = "/"
	page.PageTitle = page.T("home.page_title")
	page.PageDescription = page.T("home.page_description")
	page.MigrationCount = db.BootstrapMigrationCount()
	page.RouteGroups = h.routeGroups

	// Resolve each server's detected country once so both dashboard listings
	// (resource snapshots and scheduler jobs) can show a flag next to the name
	// without any extra per-row query.
	countries := serverCountryBadges(h.database)

	// Fleet warnings lead the dashboard: exhaustion before the traffic reset,
	// resources at/above 90%, and forecast anomalies. Dismissals are client-side
	// (localStorage) so the server always renders the full, current set.
	page.HomeWarnings = homeWarnings(r.Context(), h.database, &page)

	page.SchedulerOverview = schedulerOverviewView(h.scheduler, schedPage, 8, countries, func(p int) string {
		return homeURL(snapPage, p)
	})

	if h.database != nil && h.database.SQL != nil {
		ctx := r.Context()
		snapshotRepo := monitoring.NewSQLRepository(h.database.SQL)
		if allSnaps, err := snapshotRepo.ListAllLatestByServer(ctx); err == nil {
			total := len(allSnaps)
			totalPages, currentPage, start, end := paginateSlice(total, snapPage, homeDashboardPerPage)

			page.DashboardSnapshotTotal = total
			page.DashboardSnapshotPagination = buildPaginationView(currentPage, totalPages, func(p int) string {
				return homeURL(p, schedPage)
			})

			// Batch-fetch the latest traffic snapshot for every visible server
			// instead of issuing one query per row.
			visibleIDs := make([]int64, 0, end-start)
			for _, snapshot := range allSnaps[start:end] {
				visibleIDs = append(visibleIDs, snapshot.ServerID)
			}
			trafficByServer := make(map[int64]monitoring.TrafficSnapshot)
			if trafficSnaps, err := snapshotRepo.GetLatestTrafficByServerIDs(ctx, visibleIDs); err == nil {
				for _, ts := range trafficSnaps {
					trafficByServer[ts.ServerID] = ts
				}
			}

			page.DashboardSnapshots = make([]view.DashboardMonitoringView, 0, end-start)
			currentMonth := time.Now().UTC().Format("2006-01")
			for _, snapshot := range allSnaps[start:end] {
				v := monitoringDashboardView(snapshot)
				if badge, ok := countries[snapshot.ServerID]; ok {
					v.FlagEmoji = badge.Flag
					v.CountryName = badge.Name
				}
				if traffic, ok := trafficByServer[snapshot.ServerID]; ok && traffic.Available {
					for _, row := range traffic.MonthlyRows {
						if row.Label == currentMonth {
							v.CurrentMonthDL = dashboardFormatBytes(row.RXBytes)
							break
						}
					}
					if traffic.PeakMbps > 0 {
						v.PeakBandwidth = dashboardFormatMbps(traffic.PeakMbps)
					}
				}
				page.DashboardSnapshots = append(page.DashboardSnapshots, v)
			}
		}

		// Fleet node-status glance: one row per server with discovered nodes,
		// highlighting any that are stopped. Best-effort — a query error simply
		// omits the section.
		nodeRepo := nodes.NewSQLRepository(h.database.SQL)
		if statuses, err := nodeRepo.ListLatestNodeStatus(ctx); err == nil {
			page.DashboardNodeStatus = fleetNodeStatusView(statuses, countries)
		}
	}

	if err := h.renderer.Render(w, http.StatusOK, page); err != nil {
		http.Error(w, "render page", http.StatusInternalServerError)
	}
}

// serverCountryBadges loads every server's detected country and returns the ones
// with a renderable flag, keyed by server id. Servers without a detected country
// are omitted (callers treat a missing key as "no flag"). Any error yields an
// empty map so the page simply renders without flags. Shared by the home
// dashboard and the diagnostics scheduler view.
func serverCountryBadges(database *db.Runtime) map[int64]countryBadge {
	badges := map[int64]countryBadge{}
	if database == nil || database.SQL == nil {
		return badges
	}
	repo := servers.NewSQLRepository(database.SQL)
	list, err := repo.List(context.Background())
	if err != nil {
		return badges
	}
	for _, server := range list {
		if flag := geoip.FlagEmoji(server.CountryCode); flag != "" {
			badges[server.ID] = countryBadge{Flag: flag, Name: server.CountryName}
		}
	}
	return badges
}

// fleetNodeStatusView folds the per-server node-status summaries into the home
// dashboard view, keeping only servers that actually have discovered nodes and
// decorating each with its country flag. Servers with a stopped node sort first.
func fleetNodeStatusView(statuses []nodes.ServerNodeStatus, countries map[int64]countryBadge) view.FleetNodeStatusView {
	out := view.FleetNodeStatusView{}
	for _, st := range statuses {
		if st.Total == 0 {
			continue
		}
		state := "running"
		switch {
		case st.Stopped > 0:
			state = "stopped"
			out.HasStopped = true
		case st.Running < st.Total:
			state = "partial"
		}
		row := view.ServerNodeStatusView{
			ServerID:   st.ServerID,
			ServerName: st.ServerName,
			Total:      st.Total,
			Running:    st.Running,
			Stopped:    st.Stopped,
			Other:      st.Other,
			State:      state,
		}
		if badge, ok := countries[st.ServerID]; ok {
			row.FlagEmoji = badge.Flag
			row.CountryName = badge.Name
		}
		out.Servers = append(out.Servers, row)
	}
	// Surface servers with a stopped node first so the warning is immediately
	// visible; otherwise preserve the name ordering from the query.
	sort.SliceStable(out.Servers, func(i, j int) bool {
		return nodeStateRank(out.Servers[i].State) < nodeStateRank(out.Servers[j].State)
	})
	return out
}

// nodeStateRank orders node states worst-first for the dashboard.
func nodeStateRank(state string) int {
	switch state {
	case "stopped":
		return 0
	case "partial":
		return 1
	default:
		return 2
	}
}

// homeURL builds a home-page URL for snapshot and scheduler pagination.
func homeURL(snapPage, schedPage int) string {
	v := url.Values{}
	if snapPage > 1 {
		v.Set("snap_page", strconv.Itoa(snapPage))
	}
	if schedPage > 1 {
		v.Set("sched_page", strconv.Itoa(schedPage))
	}
	if q := v.Encode(); q != "" {
		return "/?" + q
	}
	return "/"
}

// paginateSlice clamps page into [1,totalPages] and returns start/end indices.
func paginateSlice(total, page, perPage int) (totalPages, currentPage, start, end int) {
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
	start = (page - 1) * perPage
	if start > total {
		start = total
	}
	end = start + perPage
	if end > total {
		end = total
	}
	return totalPages, page, start, end
}

// schedulerOverviewView builds a paginated scheduler view.
// makePageURL generates the URL for a given page number (called by buildPaginationView).
func schedulerOverviewView(s *scheduler.Runtime, page, perPage int, countries map[int64]countryBadge, makePageURL func(int) string) view.SchedulerOverviewView {
	if s == nil {
		return view.SchedulerOverviewView{}
	}
	if perPage <= 0 {
		perPage = 10
	}

	overview := s.Overview(0)
	allJobs := make([]view.ScheduledJobView, 0, len(overview.Jobs))
	for _, job := range overview.Jobs {
		detail := strings.TrimSpace(job.LastMessage)
		if detail == "" {
			detail = strings.TrimSpace(job.Reason)
		}
		badge := countries[job.ServerID]
		allJobs = append(allJobs, view.ScheduledJobView{
			ServerID:            job.ServerID,
			ServerName:          job.ServerName,
			FlagEmoji:           badge.Flag,
			CountryName:         badge.Name,
			JobType:             string(job.JobType),
			Status:              job.Status,
			Detail:              detail,
			LastError:           strings.TrimSpace(job.LastError),
			NextRunAt:           monitoringFormatTimestamp(job.NextRunAt),
			LastStartedAt:       monitoringFormatTimestamp(job.LastStartedAt),
			LastSuccessAt:       monitoringFormatTimestamp(job.LastSuccessAt),
			LastDuration:        formatDuration(job.LastDuration),
			ConsecutiveFailures: job.ConsecutiveFailures,
			Paused:              job.Paused,
			ToggleURL:           fmt.Sprintf("/ops/scheduler/%d/%s/toggle", job.ServerID, job.JobType),
		})
	}

	totalPages, currentPage, start, end := paginateSlice(len(allJobs), page, perPage)

	moreJobs := len(allJobs) - (end - start)
	if moreJobs < 0 {
		moreJobs = 0
	}

	return view.SchedulerOverviewView{
		Enabled:            overview.Enabled,
		StartupDelay:       formatDuration(overview.StartupDelay),
		SweepInterval:      formatDuration(overview.SweepInterval),
		MonitoringInterval: formatDuration(overview.MonitoringInterval),
		NodesInterval:      formatDuration(overview.NodesInterval),
		RetryBackoff:       formatDuration(overview.RetryBackoff),
		EligibleJobs:       overview.EligibleJobs,
		BlockedJobs:        overview.BlockedJobs,
		RunningJobs:        overview.RunningJobs,
		Jobs:               allJobs[start:end],
		MoreJobs:           moreJobs,
		Pagination:         buildPaginationView(currentPage, totalPages, makePageURL),
	}
}

// buildPaginationView is a generic pagination builder driven by a URL-maker func.
func buildPaginationView(current, total int, makeURL func(int) string) view.PaginationView {
	pages := make([]view.PaginationPageView, 0, total)
	for _, n := range pageWindowNumbers(current, total) {
		if n == 0 {
			pages = append(pages, view.PaginationPageView{IsGap: true})
			continue
		}
		pages = append(pages, view.PaginationPageView{
			Number:   n,
			URL:      makeURL(n),
			IsActive: n == current,
		})
	}
	return view.PaginationView{
		CurrentPage: current,
		TotalPages:  total,
		HasPrev:     current > 1,
		HasNext:     current < total,
		PrevURL:     makeURL(current - 1),
		NextURL:     makeURL(current + 1),
		Pages:       pages,
	}
}

func pageWindowNumbers(current, total int) []int {
	if total <= 7 {
		nums := make([]int, total)
		for i := range nums {
			nums[i] = i + 1
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

func monitoringDashboardView(snapshot monitoring.Snapshot) view.DashboardMonitoringView {
	return view.DashboardMonitoringView{
		ServerID:       snapshot.ServerID,
		ServerName:     snapshot.ServerName,
		CPUUsage:       monitoringFormatPercent(snapshot.CPUUsage),
		RAMUsage:       monitoringFormatPercent(snapshot.RAMUsage),
		DiskUsage:      monitoringFormatPercent(snapshot.DiskUsage),
		LoadAverage:    monitoringFormatLoad(snapshot.LoadAverage1) + " / " + monitoringFormatLoad(snapshot.LoadAverage5) + " / " + monitoringFormatLoad(snapshot.LoadAverage15),
		UptimeHuman:    monitoringFormatUptime(snapshot.UptimeSeconds),
		NetworkSummary: monitoringFallbackDisplay(snapshot.NetworkSummary),
		CollectedAt:    monitoringFormatTimestamp(snapshot.CreatedAt),
	}
}

func monitoringFormatPercent(value float64) string {
	return strconv.FormatFloat(value, 'f', 2, 64) + "%"
}

func monitoringFormatLoad(value float64) string {
	return strconv.FormatFloat(value, 'f', 2, 64)
}

func monitoringFormatUptime(seconds int64) string {
	if seconds <= 0 {
		return "-"
	}
	days := seconds / 86400
	seconds = seconds % 86400
	hours := seconds / 3600
	seconds = seconds % 3600
	minutes := seconds / 60
	parts := make([]string, 0, 3)
	if days > 0 {
		parts = append(parts, strconv.FormatInt(days, 10)+"d")
	}
	if hours > 0 || len(parts) > 0 {
		parts = append(parts, strconv.FormatInt(hours, 10)+"h")
	}
	parts = append(parts, strconv.FormatInt(minutes, 10)+"m")
	return strings.Join(parts, " ")
}

func monitoringFallbackDisplay(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

func monitoringFormatTimestamp(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format("2006-01-02 15:04:05 UTC")
}

func formatDuration(value time.Duration) string {
	if value <= 0 {
		return "-"
	}
	return value.Round(time.Millisecond).String()
}

func dashboardFormatBytes(value int64) string {
	if value <= 0 {
		return "-"
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	size := float64(value)
	unit := units[0]
	for i := 0; i < len(units)-1 && size >= 1024; i++ {
		size /= 1024
		unit = units[i+1]
	}
	return fmt.Sprintf("%.2f %s", size, unit)
}

func dashboardFormatMbps(mbps float64) string {
	return fmt.Sprintf("%.2f Mbps", mbps)
}
