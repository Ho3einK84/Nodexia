package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/module/alerts"
	"github.com/Ho3einK84/Nodexia/internal/module/analytics"
	"github.com/Ho3einK84/Nodexia/internal/notify"
)

// digestLoop sends a periodic status digest on its own ticker. It runs only when
// the digest is enabled AND a notifier is configured (guarded in Start). The
// first digest goes out one interval after startup, not immediately, so a restart
// loop can never spam a channel.
func (r *Runtime) digestLoop(ctx context.Context) {
	defer r.wg.Done()

	interval := r.digestCfg.Interval
	if interval <= 0 {
		interval = 24 * time.Hour
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sendDigest(ctx)
		}
	}
}

// sendDigest assembles and delivers one digest. It is best-effort: every failure
// is logged and swallowed so a digest problem can never disrupt the scheduler.
func (r *Runtime) sendDigest(ctx context.Context) {
	if r.notifier == nil {
		return
	}

	msg, err := r.collectDigest(ctx)
	if err != nil {
		slog.Warn("digest: assemble failed", slog.String("error", err.Error()))
		return
	}

	channels, err := r.digestChannels(ctx)
	if err != nil {
		slog.Warn("digest: load channels failed", slog.String("error", err.Error()))
		return
	}
	if len(channels) == 0 {
		slog.Debug("digest: no target channel — skipping send")
		return
	}

	for _, channel := range channels {
		text, err := notify.RenderDigest("", msg)
		if err != nil {
			slog.Warn("digest: render failed", slog.String("error", err.Error()))
			return
		}
		if err := r.notifier.Send(ctx, channel.ChatID, text); err != nil {
			slog.Warn("digest: send failed",
				slog.Int64("channel_id", channel.ID),
				slog.String("error", err.Error()),
			)
		}
	}
}

// digestChannels resolves which channels receive the digest. When a channel name
// is configured it must match an enabled channel (case-insensitive); otherwise
// the digest goes to every enabled channel, mirroring how a rule with no specific
// channel dispatches. A configured-but-missing channel returns none (skip send).
func (r *Runtime) digestChannels(ctx context.Context) ([]alerts.Channel, error) {
	enabled, err := r.alertsRepo.ListEnabledChannels(ctx)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(r.digestCfg.Channel)
	if name == "" {
		return enabled, nil
	}
	for _, channel := range enabled {
		if strings.EqualFold(channel.Name, name) {
			return []alerts.Channel{channel}, nil
		}
	}
	return nil, nil
}

// collectDigest builds the digest message from the SAME data the analytics
// overview exposes (server + traffic summaries + forecast) plus the currently
// active alert events. Every per-server read degrades gracefully: a server with
// no traffic, no limit, or a failed forecast still produces a row, so the digest
// never errors the scheduler over missing data.
func (r *Runtime) collectDigest(ctx context.Context) (notify.DigestMessage, error) {
	now := time.Now().UTC()
	msg := notify.DigestMessage{GeneratedAt: now.Format("2006-01-02 15:04 UTC")}

	serversList, err := r.serverRepo.List(ctx)
	if err != nil {
		return notify.DigestMessage{}, fmt.Errorf("digest: list servers: %w", err)
	}
	msg.ServerCount = len(serversList)

	// Traffic summaries: the exact rows the analytics overview shows, indexed by
	// server id. A read failure is non-fatal — rows fall back to "no data".
	trafficByID := map[int64]analytics.ServerTrafficSummary{}
	if summaries, err := r.analyticsRepo.ListServerTrafficSummaries(ctx, 0); err == nil {
		for _, s := range summaries {
			trafficByID[s.ServerID] = s
		}
	}

	// Active alerts per server.
	alertsByID := map[int64]int{}
	if open, err := r.alertsRepo.ListOpenEvents(ctx); err == nil {
		for _, ev := range open {
			alertsByID[ev.ServerID]++
		}
		msg.ActiveAlerts = len(open)
	}

	for _, server := range serversList {
		summary := trafficByID[server.ID]
		fc := r.serverForecast(ctx, server.ID)
		msg.Servers = append(msg.Servers, digestServerLine(server.Name, summary, fc, alertsByID[server.ID]))
	}

	return msg, nil
}

// serverForecast computes a server's forecast for the digest, reusing the same
// service the analytics page uses. Any failure (no traffic, no limit) yields a
// zero-value forecast whose Exhaustion.HasLimit is false, rendered as "no limit".
func (r *Runtime) serverForecast(ctx context.Context, serverID int64) analytics.ForecastOutput {
	if r.forecastSvc == nil {
		return analytics.ForecastOutput{}
	}
	days, months, err := r.analyticsRepo.GetLatestTrafficForServer(ctx, serverID)
	if err != nil || len(days) == 0 {
		return analytics.ForecastOutput{}
	}
	limitBytes, ok, err := r.analyticsRepo.GetTrafficLimit(ctx, serverID)
	if err != nil || !ok {
		limitBytes = 0
	}
	return r.forecastSvc.Compute(days, months, limitBytes)
}

// digestServerLine formats one server's digest row from its traffic summary,
// forecast, and active-alert count. Split out as a pure function so the content
// across populated/empty states is unit-testable without a DB.
func digestServerLine(name string, summary analytics.ServerTrafficSummary, fc analytics.ForecastOutput, activeAlerts int) notify.DigestServer {
	download := "no data yet"
	total := "no data yet"
	if summary.MonthBytes > 0 || summary.MonthRX > 0 || summary.MonthTX > 0 {
		download = formatBytes(summary.MonthRX)
		monthTotal := summary.MonthBytes
		if monthTotal == 0 {
			monthTotal = summary.MonthRX + summary.MonthTX
		}
		total = formatBytes(monthTotal)
	}

	return notify.DigestServer{
		Name:          name,
		MonthDownload: download,
		MonthTotal:    total,
		LimitState:    digestLimitState(fc),
		ActiveAlerts:  activeAlerts,
	}
}

// digestLimitState renders a one-line summary of a server's monthly-limit
// forecast state, mirroring the analytics exhaustion projection.
func digestLimitState(fc analytics.ForecastOutput) string {
	ex := fc.Exhaustion
	switch {
	case !ex.HasLimit:
		return "No monthly download limit set"
	case ex.AlreadyOver:
		return fmt.Sprintf("⚠️ Monthly limit already exceeded (limit %s)", formatBytes(ex.LimitBytes))
	case ex.WillExhaust:
		return fmt.Sprintf("⚠️ Projected to reach limit in %d day(s) on %s (limit %s)", ex.DaysRemaining, ex.ExhaustionDate, formatBytes(ex.LimitBytes))
	default:
		return fmt.Sprintf("On track to stay under limit (%s)", formatBytes(ex.LimitBytes))
	}
}

// formatBytes renders a byte count as a human-readable size, matching the
// analytics module's own formatting so the digest reads like the overview.
func formatBytes(b int64) string {
	if b <= 0 {
		return "0 B"
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	size := float64(b)
	unit := units[0]
	for i := 0; i < len(units)-1 && size >= 1024; i++ {
		size /= 1024
		unit = units[i+1]
	}
	return fmt.Sprintf("%.2f %s", size, unit)
}
