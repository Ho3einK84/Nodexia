package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/backup"
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
	h.renderPage(w, r, http.StatusOK, "", "")
}

// renderPage renders the diagnostics page, optionally with a flash banner.
// Export/restore handlers reuse it so their results land back on the same page.
func (h DiagnosticsHandler) renderPage(w http.ResponseWriter, r *http.Request, status int, flashKind, flashMessage string) {
	page := view.NewPageData(h.config, r)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = page.T("diagnostics.title")
	page.ContentTemplate = "content-diagnostics"
	page.ActiveNav = "/ops/diagnostics"
	page.PageTitle = page.T("diagnostics.page_title")
	page.PageDescription = page.T("diagnostics.page_description")
	page.FlashKind = flashKind
	page.FlashMessage = flashMessage
	page.BackupCanRun = h.database != nil && h.database.SQL != nil
	page.PageStyles = []string{"/static/diagnostics.css"}
	page.PageScripts = []string{"/static/diagnostics.js"}
	if page.BackupCanRun {
		if counts, err := backup.SummarizeDB(r.Context(), h.database.Reader()); err == nil {
			page.BackupOverview = view.BackupOverviewView{
				Servers:             counts.Servers,
				ServerTags:          counts.ServerTags,
				ServerTrafficLimits: counts.ServerTrafficLimits,
				TrafficLimitRules:   counts.TrafficLimitRules,
				AlertChannels:       counts.AlertChannels,
				AlertRules:          counts.AlertRules,
				AlertSilences:       counts.AlertSilences,
				InstallMetadata:     counts.InstallMetadata,
				SecretEnvCount:      len(h.secretEnv()),
			}
		}
	}
	schPage, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("sch_page")))
	page.Diagnostics = h.buildDiagnostics(r)
	page.SchedulerOverview = schedulerOverviewView(h.scheduler, schPage, 10, serverCountryBadges(r.Context(), h.database), func(p int) string {
		if p <= 1 {
			return "/ops/diagnostics"
		}
		return "/ops/diagnostics?sch_page=" + strconv.Itoa(p)
	})
	if h.scheduler != nil {
		page.SchedulerOverview.RecentRuns = schedulerRunViews(h.scheduler.RecentRuns(r.Context(), 20))
	}

	if err := h.renderer.Render(w, status, page); err != nil {
		http.Error(w, "render diagnostics page", http.StatusInternalServerError)
	}
}

// SchedulerToggle handles POST /ops/scheduler/{serverID}/{jobType}/toggle.
// It pauses a running job or resumes a paused one, returning JSON when the
// client asks for application/json or redirecting back for plain form submits.
func (h DiagnosticsHandler) SchedulerToggle(w http.ResponseWriter, r *http.Request) {
	asJSON := strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json")
	serverIDStr := strings.TrimSpace(r.PathValue("serverID"))
	jobTypeStr := strings.TrimSpace(r.PathValue("jobType"))

	serverID, err := strconv.ParseInt(serverIDStr, 10, 64)
	if err != nil || serverID < 1 {
		if asJSON {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "message": "Invalid server ID"})
			return
		}
		http.Redirect(w, r, "/ops/diagnostics", http.StatusSeeOther)
		return
	}

	paused := false
	if h.scheduler != nil {
		paused = h.scheduler.ToggleJob(serverID, scheduler.JobType(jobTypeStr))
	}

	if asJSON {
		page := view.NewPageData(h.config, r)
		statusLabel := "scheduled"
		statusVariant := "ok"
		buttonLabel := page.T("common.disable")
		buttonTitle := page.T("diagnostics.pause_title")
		msg := page.T("diagnostics.job_resumed")
		if paused {
			statusLabel = page.T("common.paused")
			statusVariant = "idle"
			buttonLabel = page.T("common.enable")
			buttonTitle = page.T("diagnostics.resume_title")
			msg = page.T("diagnostics.job_paused")
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":             true,
			"paused":         paused,
			"status_label":   statusLabel,
			"status_variant": statusVariant,
			"button_label":   buttonLabel,
			"button_title":   buttonTitle,
			"message":        msg,
		})
		return
	}

	http.Redirect(w, r, "/ops/diagnostics", http.StatusSeeOther)
}

// schedulerRunViews converts persisted job runs into display rows. A failed
// run's detail is its error; a successful run shows its message.
func schedulerRunViews(runs []scheduler.JobRun) []view.SchedulerRunView {
	out := make([]view.SchedulerRunView, 0, len(runs))
	for _, run := range runs {
		name := run.ServerName
		if name == "" {
			name = "#" + strconv.FormatInt(run.ServerID, 10)
		}
		detail := run.Message
		if !run.Success {
			detail = run.Error
		}
		out = append(out, view.SchedulerRunView{
			ServerName: name,
			JobType:    string(run.JobType),
			Success:    run.Success,
			StartedAt:  monitoringFormatTimestamp(run.StartedAt),
			Duration:   formatDuration(run.Duration),
			Detail:     detail,
		})
	}
	return out
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
