package handlers

import (
	"context"
	"net/http"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/commandstream"
	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/db"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	nodexiaruntime "github.com/Ho3einK84/Nodexia/internal/runtime"
	"github.com/Ho3einK84/Nodexia/internal/scheduler"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

type DiagnosticsHandler struct {
	config         config.Config
	database       *db.Runtime
	renderer       *view.Renderer
	scheduler      *scheduler.Runtime
	commandStreams *commandstream.Store
}

func NewDiagnosticsHandler(cfg config.Config, database *db.Runtime, renderer *view.Renderer, backgroundScheduler *scheduler.Runtime, commandStreams *commandstream.Store) DiagnosticsHandler {
	return DiagnosticsHandler{
		config:         cfg,
		database:       database,
		renderer:       renderer,
		scheduler:      backgroundScheduler,
		commandStreams: commandStreams,
	}
}

func (h DiagnosticsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	page := view.NewPageData(h.config, r)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = page.T("diagnostics.title")
	page.ContentTemplate = "content-diagnostics"
	page.ActiveNav = "/ops/diagnostics"
	page.PageTitle = page.T("diagnostics.page_title")
	page.PageDescription = page.T("diagnostics.page_description")
	schPage, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("sch_page")))
	page.Diagnostics = h.buildDiagnostics(r)
	page.SchedulerOverview = schedulerOverviewView(h.scheduler, schPage, 10, serverCountryBadges(h.database), func(p int) string {
		if p <= 1 {
			return "/ops/diagnostics"
		}
		return "/ops/diagnostics?sch_page=" + strconv.Itoa(p)
	})

	if err := h.renderer.Render(w, http.StatusOK, page); err != nil {
		http.Error(w, "render diagnostics page", http.StatusInternalServerError)
	}
}

// SchedulerToggle handles POST /ops/scheduler/{serverID}/{jobType}/toggle.
// It pauses a running job or resumes a paused one, then redirects back.
func (h DiagnosticsHandler) SchedulerToggle(w http.ResponseWriter, r *http.Request) {
	serverIDStr := strings.TrimSpace(r.PathValue("serverID"))
	jobTypeStr := strings.TrimSpace(r.PathValue("jobType"))

	serverID, err := strconv.ParseInt(serverIDStr, 10, 64)
	if err != nil || serverID < 1 {
		http.Redirect(w, r, "/ops/diagnostics", http.StatusSeeOther)
		return
	}

	if h.scheduler != nil {
		h.scheduler.ToggleJob(serverID, scheduler.JobType(jobTypeStr))
	}

	http.Redirect(w, r, "/ops/diagnostics", http.StatusSeeOther)
}

func (h DiagnosticsHandler) buildDiagnostics(r *http.Request) view.DiagnosticsView {
	uptime := time.Since(nodexiaruntime.StartedAt).Round(time.Second)
	dbStatus := "unavailable"
	dbDetail := "database runtime is not configured"
	migrationCount := 0

	if h.database != nil {
		migrationCount = h.database.MigrationCount()
		if h.database.SQL != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := h.database.SQL.PingContext(ctx); err != nil {
				dbStatus = "fail"
				dbDetail = err.Error()
			} else {
				dbStatus = "ok"
				dbDetail = h.config.Database.Driver
			}
		}
	}

	streams := 0
	if h.commandStreams != nil {
		streams = h.commandStreams.ActiveCount()
	}

	return view.DiagnosticsView{
		StartedAt:          nodexiaruntime.StartedAt.UTC().Format(time.RFC3339),
		Uptime:             uptime.String(),
		GoVersion:          goruntime.Version(),
		NumCPU:             goruntime.NumCPU(),
		Goroutines:         goruntime.NumGoroutine(),
		DatabaseStatus:     dbStatus,
		DatabaseDetail:     dbDetail,
		MigrationCount:     migrationCount,
		CommandStreamCount: streams,
		HealthLiveURL:      "/healthz/live",
		HealthReadyURL:     "/healthz/ready",
		SSHHostKeyPolicy:   h.config.Security.SSHHostKeyPolicy,
		SchedulerEnabled:   h.config.Scheduler.Enabled,
		BehindReverseProxy: h.config.Install.BehindReverseProxy,
	}
}
