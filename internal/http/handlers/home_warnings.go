package handlers

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/db"
	"github.com/Ho3einK84/Nodexia/internal/module/analytics"
	"github.com/Ho3einK84/Nodexia/internal/module/monitoring"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

// resourceWarnThreshold is the usage percentage at which a resource warning
// banner appears on the home dashboard.
const resourceWarnThreshold = 90.0

// homeWarnings computes the fleet warning banners for the home dashboard:
// projected/actual traffic exhaustion before the period reset, any resource at
// or above 90%, and forecast anomalies (spike / unusual growth). Everything is
// best-effort — a read failure simply omits its warnings — and the whole pass
// reuses data the dashboard already stores (no SSH).
//
// Banner ordering is severity-first (danger before warning), then server name,
// so the most urgent problem always leads.
func homeWarnings(ctx context.Context, database *db.Runtime, loc interface {
	T(key string, args ...any) string
}) []view.HomeWarningView {
	if database == nil || database.SQL == nil {
		return nil
	}

	serverRepo := servers.NewSQLRepository(database.SQL)
	serversList, err := serverRepo.List(ctx)
	if err != nil || len(serversList) == 0 {
		return nil
	}

	analyticsRepo := analytics.NewSQLRepository(database.SQL)
	monitoringRepo := monitoring.NewSQLRepository(database.SQL)
	forecastSvc := analytics.NewForecastService()

	// Latest resource snapshot per server, indexed by id.
	snapshotByID := map[int64]monitoring.Snapshot{}
	if snaps, err := monitoringRepo.ListAllLatestByServer(ctx); err == nil {
		for _, snap := range snaps {
			snapshotByID[snap.ServerID] = snap
		}
	}

	var warnings []view.HomeWarningView
	for _, server := range serversList {
		warnings = append(warnings, resourceWarnings(loc, server, snapshotByID)...)
		warnings = append(warnings, trafficWarnings(ctx, loc, analyticsRepo, forecastSvc, server)...)
	}

	sort.SliceStable(warnings, func(i, j int) bool {
		if warnings[i].Severity != warnings[j].Severity {
			return warnings[i].Severity == "danger"
		}
		return warnings[i].ServerName < warnings[j].ServerName
	})
	return warnings
}

// resourceWarnings flags any of CPU/RAM/disk at or above the threshold in the
// server's latest stored snapshot.
func resourceWarnings(loc interface {
	T(key string, args ...any) string
}, server servers.Server, snapshots map[int64]monitoring.Snapshot) []view.HomeWarningView {
	snap, ok := snapshots[server.ID]
	if !ok {
		return nil
	}

	var out []view.HomeWarningView
	add := func(metricKey string, value float64) {
		if value < resourceWarnThreshold {
			return
		}
		severity := "warning"
		icon := "gauge"
		if value >= 95 {
			severity = "danger"
			icon = "alert-octagon"
		}
		out = append(out, view.HomeWarningView{
			ID:         fmt.Sprintf("res:%d:%s", server.ID, metricKey),
			Severity:   severity,
			Icon:       icon,
			ServerID:   server.ID,
			ServerName: server.Name,
			Message: loc.T("home.warn.resource",
				"server", server.Name,
				"metric", loc.T("home.warn.metric_"+metricKey),
				"value", fmt.Sprintf("%.0f%%", value)),
			ActionURL:   fmt.Sprintf("/servers/%d/monitoring", server.ID),
			ActionLabel: loc.T("home.warn.open_monitoring"),
		})
	}
	add("cpu", snap.CPUUsage)
	add("ram", snap.RAMUsage)
	add("disk", snap.DiskUsage)
	return out
}

// trafficWarnings runs the shared forecast for one server and flags exhaustion
// before the period reset plus anomaly risks. Servers without traffic history
// produce nothing; the forecast honours the billing anchor and limit kind.
func trafficWarnings(ctx context.Context, loc interface {
	T(key string, args ...any) string
}, repo analytics.Repository, svc *analytics.ForecastService, server servers.Server) []view.HomeWarningView {
	days, months, err := repo.GetLatestTrafficForServer(ctx, server.ID)
	if err != nil || len(days) == 0 {
		return nil
	}
	limit, _, ok, err := repo.ResolveEffectiveLimit(ctx, server.ID, server.Tags)
	if err != nil || !ok {
		limit = analytics.TrafficLimit{}
	}
	out := svc.ComputeWithConfig(days, months, analytics.ForecastConfig{Limit: limit, ResetDay: server.TrafficResetDay})

	var warnings []view.HomeWarningView
	analyticsURL := fmt.Sprintf("/servers/%d/analytics", server.ID)

	ex := out.Exhaustion
	switch {
	case ex.HasLimit && ex.AlreadyOver:
		warnings = append(warnings, view.HomeWarningView{
			ID: fmt.Sprintf("exh:%d:over", server.ID), Severity: "danger", Icon: "alert-octagon",
			ServerID: server.ID, ServerName: server.Name,
			Message:   loc.T("home.warn.limit_over", "server", server.Name, "limit", formatWarnBytes(ex.LimitBytes)),
			ActionURL: analyticsURL, ActionLabel: loc.T("home.warn.open_analytics"),
		})
	case ex.HasLimit && ex.WillExhaust:
		severity := "warning"
		icon := "alert-triangle"
		if ex.DaysRemaining <= 3 {
			severity = "danger"
			icon = "alert-octagon"
		}
		warnings = append(warnings, view.HomeWarningView{
			ID: fmt.Sprintf("exh:%d:%s", server.ID, ex.ExhaustionDate), Severity: severity, Icon: icon,
			ServerID: server.ID, ServerName: server.Name,
			Message: loc.T("home.warn.limit_exhaust",
				"server", server.Name,
				"days", fmt.Sprintf("%d", ex.DaysRemaining),
				"date", ex.ExhaustionDate,
				"reset", fmt.Sprintf("%d", ex.DaysUntilMonthEnd)),
			ActionURL: analyticsURL, ActionLabel: loc.T("home.warn.open_analytics"),
		})
	}

	if out.Risks.TrafficSpike {
		warnings = append(warnings, view.HomeWarningView{
			ID: fmt.Sprintf("spike:%d:%s", server.ID, time.Now().UTC().Format("2006-01-02")), Severity: "warning", Icon: "trending-up",
			ServerID: server.ID, ServerName: server.Name,
			Message:   loc.T("home.warn.spike", "server", server.Name),
			ActionURL: analyticsURL, ActionLabel: loc.T("home.warn.open_analytics"),
		})
	}
	if out.Risks.UnusualGrowth {
		warnings = append(warnings, view.HomeWarningView{
			ID: fmt.Sprintf("growth:%d:%s", server.ID, out.PeriodStart), Severity: "warning", Icon: "chart-spline",
			ServerID: server.ID, ServerName: server.Name,
			Message:   loc.T("home.warn.growth", "server", server.Name),
			ActionURL: analyticsURL, ActionLabel: loc.T("home.warn.open_analytics"),
		})
	}
	return warnings
}

func formatWarnBytes(b int64) string {
	return dashboardFormatBytes(b)
}
