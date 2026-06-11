package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/db"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/module/monitoring"
	"github.com/Ho3einK84/Nodexia/internal/scheduler"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

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

	page := view.NewPageData(h.config)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = "Nodexia"
	page.ContentTemplate = "content-home"
	page.ActiveNav = "/"
	page.PageTitle = "Operations overview"
	page.PageDescription = "Resource health, scheduled collection, and runtime status across your managed Rebecca and PasarGuard servers."
	page.MigrationCount = db.BootstrapMigrationCount()
	page.RouteGroups = h.routeGroups

	page.SchedulerOverview = schedulerOverviewView(h.scheduler, 1, 8, func(p int) string {
		return homeURL(snapPage, p)
	})

	if h.database != nil && h.database.SQL != nil {
		ctx := context.Background()
		snapshotRepo := monitoring.NewSQLRepository(h.database.SQL)
		if allSnaps, err := snapshotRepo.ListAllLatestByServer(ctx); err == nil {
			total := len(allSnaps)
			totalPages, currentPage, start, end := paginateSlice(total, snapPage, homeDashboardPerPage)

			page.DashboardSnapshotTotal = total
			page.DashboardSnapshotPagination = buildPaginationView(currentPage, totalPages, func(p int) string {
				return homeURL(p, 0)
			})
			page.DashboardSnapshots = make([]view.DashboardMonitoringView, 0, end-start)
			for _, snapshot := range allSnaps[start:end] {
				v := monitoringDashboardView(snapshot)
				if traffic, err := snapshotRepo.GetLatestTrafficByServer(ctx, snapshot.ServerID); err == nil && traffic.Available {
					currentMonth := time.Now().UTC().Format("2006-01")
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
	}

	if err := h.renderer.Render(w, http.StatusOK, page); err != nil {
		http.Error(w, "render page", http.StatusInternalServerError)
	}
}

// homeURL builds a home-page URL for snapshot pagination.
func homeURL(snapPage, _ int) string {
	v := url.Values{}
	if snapPage > 1 {
		v.Set("snap_page", strconv.Itoa(snapPage))
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
func schedulerOverviewView(s *scheduler.Runtime, page, perPage int, makePageURL func(int) string) view.SchedulerOverviewView {
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
		allJobs = append(allJobs, view.ScheduledJobView{
			ServerID:            job.ServerID,
			ServerName:          job.ServerName,
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
