package handlers

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/db"
	"github.com/Ho3einK84/Nodexia/internal/module/alerts"
	"github.com/Ho3einK84/Nodexia/internal/module/monitoring"
	"github.com/Ho3einK84/Nodexia/internal/module/nodes"
	"github.com/Ho3einK84/Nodexia/internal/scheduler"
)

// MetricsHandler serves GET /metrics in the Prometheus text exposition format,
// hand-rolled over the stdlib (no client library). It is DISABLED unless
// NODEXIA_METRICS_TOKEN is configured, and every request must present that
// token (Authorization: Bearer <token> or ?token=) — fleet metrics are
// sensitive, and scrapers cannot carry the panel's session cookies.
type MetricsHandler struct {
	config    config.Config
	database  *db.Runtime
	scheduler *scheduler.Runtime
}

func NewMetricsHandler(cfg config.Config, database *db.Runtime, backgroundScheduler *scheduler.Runtime) MetricsHandler {
	return MetricsHandler{config: cfg, database: database, scheduler: backgroundScheduler}
}

func (h MetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(h.config.Security.MetricsToken)
	if token == "" {
		// Not configured → the endpoint does not exist.
		http.NotFound(w, r)
		return
	}
	if !metricsTokenMatches(r, token) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="nodexia-metrics"`)
		http.Error(w, "metrics: invalid or missing token", http.StatusUnauthorized)
		return
	}

	var b strings.Builder
	h.writeBuildInfo(&b)
	h.writeFleetMetrics(r.Context(), &b)
	h.writeSchedulerMetrics(&b)

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(b.String()))
}

// metricsTokenMatches accepts the token from the Authorization: Bearer header
// or, for scrapers that cannot set headers, the ?token= query parameter.
func metricsTokenMatches(r *http.Request, expected string) bool {
	presented := ""
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); auth != "" {
		if bearer, ok := strings.CutPrefix(auth, "Bearer "); ok {
			presented = strings.TrimSpace(bearer)
		}
	}
	if presented == "" {
		presented = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	return presented != "" && subtle.ConstantTimeCompare([]byte(presented), []byte(expected)) == 1
}

func (h MetricsHandler) writeBuildInfo(b *strings.Builder) {
	fmt.Fprintf(b, "# HELP nodexia_build_info Build information.\n# TYPE nodexia_build_info gauge\n")
	fmt.Fprintf(b, "nodexia_build_info{version=%q} 1\n", h.config.Version)
}

// writeFleetMetrics exposes the latest stored snapshot per server (resource
// usage), the current-month traffic totals, node status counts, and the open
// alert count. Every read is best-effort: a failed query just omits its family.
func (h MetricsHandler) writeFleetMetrics(ctx context.Context, b *strings.Builder) {
	if h.database == nil || h.database.SQL == nil {
		return
	}

	if snaps, err := monitoring.NewSQLRepository(h.database.SQL).ListAllLatestByServer(ctx); err == nil && len(snaps) > 0 {
		fmt.Fprintf(b, "# HELP nodexia_server_cpu_percent Latest stored CPU usage per server.\n# TYPE nodexia_server_cpu_percent gauge\n")
		for _, s := range snaps {
			fmt.Fprintf(b, "nodexia_server_cpu_percent{server=%q} %.2f\n", s.ServerName, s.CPUUsage)
		}
		fmt.Fprintf(b, "# HELP nodexia_server_ram_percent Latest stored RAM usage per server.\n# TYPE nodexia_server_ram_percent gauge\n")
		for _, s := range snaps {
			fmt.Fprintf(b, "nodexia_server_ram_percent{server=%q} %.2f\n", s.ServerName, s.RAMUsage)
		}
		fmt.Fprintf(b, "# HELP nodexia_server_disk_percent Latest stored disk usage per server.\n# TYPE nodexia_server_disk_percent gauge\n")
		for _, s := range snaps {
			fmt.Fprintf(b, "nodexia_server_disk_percent{server=%q} %.2f\n", s.ServerName, s.DiskUsage)
		}
		fmt.Fprintf(b, "# HELP nodexia_server_load1 Latest stored 1-minute load average per server.\n# TYPE nodexia_server_load1 gauge\n")
		for _, s := range snaps {
			fmt.Fprintf(b, "nodexia_server_load1{server=%q} %.2f\n", s.ServerName, s.LoadAverage1)
		}
	}

	if open, err := alerts.NewSQLRepository(h.database.SQL).ListOpenEvents(ctx); err == nil {
		fmt.Fprintf(b, "# HELP nodexia_alerts_open Currently firing alert events.\n# TYPE nodexia_alerts_open gauge\n")
		fmt.Fprintf(b, "nodexia_alerts_open %d\n", len(open))
	}

	if statuses, err := nodes.NewSQLRepository(h.database.SQL).ListLatestNodeStatus(ctx); err == nil && len(statuses) > 0 {
		fmt.Fprintf(b, "# HELP nodexia_nodes_running Running nodes per server (latest discovery).\n# TYPE nodexia_nodes_running gauge\n")
		for _, st := range statuses {
			if st.Total == 0 {
				continue
			}
			fmt.Fprintf(b, "nodexia_nodes_running{server=%q} %d\n", st.ServerName, st.Running)
		}
		fmt.Fprintf(b, "# HELP nodexia_nodes_stopped Stopped nodes per server (latest discovery).\n# TYPE nodexia_nodes_stopped gauge\n")
		for _, st := range statuses {
			if st.Total == 0 {
				continue
			}
			fmt.Fprintf(b, "nodexia_nodes_stopped{server=%q} %d\n", st.ServerName, st.Stopped)
		}
	}
}

// writeSchedulerMetrics exposes the in-memory scheduler job states.
func (h MetricsHandler) writeSchedulerMetrics(b *strings.Builder) {
	if h.scheduler == nil {
		return
	}
	overview := h.scheduler.Overview(0)
	fmt.Fprintf(b, "# HELP nodexia_scheduler_jobs Scheduler jobs by state.\n# TYPE nodexia_scheduler_jobs gauge\n")
	fmt.Fprintf(b, "nodexia_scheduler_jobs{state=\"eligible\"} %d\n", overview.EligibleJobs)
	fmt.Fprintf(b, "nodexia_scheduler_jobs{state=\"blocked\"} %d\n", overview.BlockedJobs)
	fmt.Fprintf(b, "nodexia_scheduler_jobs{state=\"running\"} %d\n", overview.RunningJobs)

	failures := map[string]int{}
	for _, job := range overview.Jobs {
		if job.ConsecutiveFailures > 0 {
			failures[job.ServerName] += job.ConsecutiveFailures
		}
	}
	if len(failures) > 0 {
		names := make([]string, 0, len(failures))
		for name := range failures {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Fprintf(b, "# HELP nodexia_scheduler_consecutive_failures Consecutive job failures per server.\n# TYPE nodexia_scheduler_consecutive_failures gauge\n")
		for _, name := range names {
			fmt.Fprintf(b, "nodexia_scheduler_consecutive_failures{server=%q} %d\n", name, failures[name])
		}
	}
}
