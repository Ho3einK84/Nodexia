package handlers

import (
	"fmt"
	"sort"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/humanize"
	"github.com/Ho3einK84/Nodexia/internal/module/analytics"
	"github.com/Ho3einK84/Nodexia/internal/module/monitoring"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

// resourceWarnThreshold is the usage percentage at which a resource warning
// banner appears on the home dashboard.
const resourceWarnThreshold = 90.0

// Banner ordering is severity-first (danger before warning), then server name,
// so the most urgent problem always leads. Warnings cover projected/actual
// traffic exhaustion, any resource at or above 90%, and forecast anomalies.
// computeHomeWarnings computes the fleet warning banners from preloaded
// in-memory snapshots, traffic rows, and traffic limits (zero SQL queries).
func computeHomeWarnings(
	serversList []servers.Server,
	snapshotByID map[int64]monitoring.Snapshot,
	trafficByServer map[int64]monitoring.TrafficSnapshot,
	limitResolver *analytics.LimitResolver,
	loc interface {
		T(key string, args ...any) string
	},
) []view.HomeWarningView {
	if len(serversList) == 0 {
		return nil
	}
	forecastSvc := analytics.NewForecastService()

	var warnings []view.HomeWarningView
	for _, server := range serversList {
		warnings = append(warnings, resourceWarnings(loc, server, snapshotByID)...)
		if traffic, ok := trafficByServer[server.ID]; ok && traffic.Available && len(traffic.DailyRows) > 0 {
			days := make([]analytics.TrafficDay, 0, len(traffic.DailyRows))
			for _, d := range traffic.DailyRows {
				total := d.TotalBytes
				if total == 0 {
					total = d.RXBytes + d.TXBytes
				}
				days = append(days, analytics.TrafficDay{Label: d.Label, RX: d.RXBytes, TX: d.TXBytes, Total: total})
			}
			months := make([]analytics.TrafficMonth, 0, len(traffic.MonthlyRows))
			for _, m := range traffic.MonthlyRows {
				total := m.TotalBytes
				if total == 0 {
					total = m.RXBytes + m.TXBytes
				}
				months = append(months, analytics.TrafficMonth{Label: m.Label, RX: m.RXBytes, TX: m.TXBytes, Total: total})
			}
			limit, _, _ := limitResolver.Resolve(server.ID, server.Tags)
			warnings = append(warnings, trafficWarningsFromData(loc, forecastSvc, server, days, months, limit)...)
		}
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

// trafficWarningsFromData runs the shared forecast for one server using
// preloaded traffic rows and effective limit.
func trafficWarningsFromData(loc interface {
	T(key string, args ...any) string
}, svc *analytics.ForecastService, server servers.Server, days []analytics.TrafficDay, months []analytics.TrafficMonth, limit analytics.TrafficLimit) []view.HomeWarningView {
	if len(days) == 0 {
		return nil
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
			Message:   loc.T("home.warn.limit_over", "server", server.Name, "limit", humanize.Bytes(ex.LimitBytes, humanize.WithNonPositiveFallback("-"))),
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
