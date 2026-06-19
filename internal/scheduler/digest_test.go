package scheduler

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/module/alerts"
	"github.com/Ho3einK84/Nodexia/internal/module/analytics"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
)

const gib = int64(1024 * 1024 * 1024)

func TestDigestLimitState(t *testing.T) {
	tests := map[string]struct {
		ex   analytics.ExhaustionForecast
		want string
	}{
		"no limit":     {ex: analytics.ExhaustionForecast{HasLimit: false}, want: "No monthly download limit set"},
		"already over": {ex: analytics.ExhaustionForecast{HasLimit: true, AlreadyOver: true, LimitBytes: 500 * gib}, want: "already exceeded"},
		"will exhaust": {ex: analytics.ExhaustionForecast{HasLimit: true, WillExhaust: true, DaysRemaining: 3, ExhaustionDate: "2026-06-22", LimitBytes: 500 * gib}, want: "Projected to reach limit in 3 day(s) on 2026-06-22"},
		"safe":         {ex: analytics.ExhaustionForecast{HasLimit: true, LimitBytes: 500 * gib}, want: "On track to stay under limit"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := digestLimitState(analytics.ForecastOutput{Exhaustion: tc.ex})
			if !strings.Contains(got, tc.want) {
				t.Fatalf("digestLimitState() = %q, want containing %q", got, tc.want)
			}
		})
	}
}

func TestDigestServerLineEmptyTraffic(t *testing.T) {
	line := digestServerLine("edge-1", analytics.ServerTrafficSummary{}, analytics.ForecastOutput{}, 0)
	if line.MonthDownload != "no data yet" || line.MonthTotal != "no data yet" {
		t.Fatalf("expected no-data placeholders, got %#v", line)
	}
	if line.LimitState != "No monthly download limit set" {
		t.Fatalf("LimitState = %q", line.LimitState)
	}
	if line.ActiveAlerts != 0 {
		t.Fatalf("ActiveAlerts = %d, want 0", line.ActiveAlerts)
	}
}

func TestDigestServerLinePopulated(t *testing.T) {
	summary := analytics.ServerTrafficSummary{MonthRX: 120 * gib, MonthTX: 30 * gib, MonthBytes: 150 * gib}
	fc := analytics.ForecastOutput{Exhaustion: analytics.ExhaustionForecast{HasLimit: true, LimitBytes: 500 * gib}}
	line := digestServerLine("edge-1", summary, fc, 2)
	if !strings.Contains(line.MonthDownload, "GiB") || line.MonthDownload == "no data yet" {
		t.Fatalf("MonthDownload = %q, want a GiB value", line.MonthDownload)
	}
	if line.ActiveAlerts != 2 {
		t.Fatalf("ActiveAlerts = %d, want 2", line.ActiveAlerts)
	}
	if !strings.Contains(line.LimitState, "On track") {
		t.Fatalf("LimitState = %q", line.LimitState)
	}
}

func monthLabel() string { return time.Now().UTC().Format("2006-01") }

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func TestCollectDigest(t *testing.T) {
	runtime := testutil.OpenTestDB(t)
	ctx := context.Background()
	r := &Runtime{
		serverRepo:    servers.NewSQLRepository(runtime.SQL),
		alertsRepo:    alerts.NewSQLRepository(runtime.SQL),
		analyticsRepo: analytics.NewSQLRepository(runtime.SQL),
		forecastSvc:   analytics.NewForecastService(),
	}

	serverRepo := servers.NewSQLRepository(runtime.SQL)
	srv, err := serverRepo.Create(ctx, servers.Server{
		Name: "edge-1", Host: "203.0.113.5", Port: 22,
		AuthMode: servers.AuthModePassword, Username: "ubuntu",
		CredentialStrategy: servers.CredentialStrategyRuntime,
	})
	if err != nil {
		t.Fatalf("Create server: %v", err)
	}
	// A second server with no traffic at all — must still render gracefully.
	if _, err := serverRepo.Create(ctx, servers.Server{
		Name: "edge-2", Host: "203.0.113.6", Port: 22,
		AuthMode: servers.AuthModePassword, Username: "ubuntu",
		CredentialStrategy: servers.CredentialStrategyRuntime,
	}); err != nil {
		t.Fatalf("Create server 2: %v", err)
	}

	month := monthLabel()
	monthlyJSON := `[{"label":"` + month + `","rx_bytes":` + itoa(120*gib) + `,"tx_bytes":` + itoa(30*gib) + `,"total_bytes":` + itoa(150*gib) + `}]`
	dailyJSON := `[{"label":"2026-06-01","rx_bytes":` + itoa(4*gib) + `,"tx_bytes":1,"total_bytes":` + itoa(4*gib) + `}]`
	if _, err := runtime.SQL.ExecContext(ctx,
		`INSERT INTO vnstat_snapshots (server_id, available, daily_rows_json, monthly_rows_json) VALUES (?, 1, ?, ?)`,
		srv.ID, dailyJSON, monthlyJSON,
	); err != nil {
		t.Fatalf("insert vnstat: %v", err)
	}

	// An open (firing) alert event for edge-1.
	if _, err := r.alertsRepo.CreateEvent(ctx, alerts.Event{
		ServerID: srv.ID, Metric: alerts.MetricCPU, ObservedValue: 99, Threshold: 90,
		Severity: alerts.SeverityWarning, State: alerts.EventStateFiring,
	}); err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}

	msg, err := r.collectDigest(ctx)
	if err != nil {
		t.Fatalf("collectDigest() error = %v", err)
	}
	if msg.ServerCount != 2 {
		t.Fatalf("ServerCount = %d, want 2", msg.ServerCount)
	}
	if msg.ActiveAlerts != 1 {
		t.Fatalf("ActiveAlerts = %d, want 1", msg.ActiveAlerts)
	}
	if len(msg.Servers) != 2 {
		t.Fatalf("Servers = %d, want 2", len(msg.Servers))
	}

	var edge1, edge2 bool
	for _, s := range msg.Servers {
		switch s.Name {
		case "edge-1":
			edge1 = true
			if s.ActiveAlerts != 1 {
				t.Fatalf("edge-1 ActiveAlerts = %d, want 1", s.ActiveAlerts)
			}
			if !strings.Contains(s.MonthDownload, "GiB") {
				t.Fatalf("edge-1 MonthDownload = %q", s.MonthDownload)
			}
		case "edge-2":
			edge2 = true
			if s.MonthDownload != "no data yet" {
				t.Fatalf("edge-2 MonthDownload = %q, want no-data placeholder", s.MonthDownload)
			}
		}
	}
	if !edge1 || !edge2 {
		t.Fatalf("missing server rows: edge1=%v edge2=%v", edge1, edge2)
	}
}

func TestDigestChannels(t *testing.T) {
	runtime := testutil.OpenTestDB(t)
	ctx := context.Background()
	alertsRepo := alerts.NewSQLRepository(runtime.SQL)

	if _, err := alertsRepo.CreateChannel(ctx, alerts.Channel{Kind: alerts.ChannelKindTelegram, Name: "Ops", ChatID: "-100", Enabled: true}); err != nil {
		t.Fatalf("seed Ops: %v", err)
	}
	if _, err := alertsRepo.CreateChannel(ctx, alerts.Channel{Kind: alerts.ChannelKindTelegram, Name: "Reports", ChatID: "-200", Enabled: true}); err != nil {
		t.Fatalf("seed Reports: %v", err)
	}
	if _, err := alertsRepo.CreateChannel(ctx, alerts.Channel{Kind: alerts.ChannelKindTelegram, Name: "Disabled", ChatID: "-300", Enabled: false}); err != nil {
		t.Fatalf("seed Disabled: %v", err)
	}

	mk := func(name string) *Runtime {
		return &Runtime{alertsRepo: alertsRepo, digestCfg: config.DigestConfig{Channel: name}}
	}

	// Empty channel name → every enabled channel.
	if got, err := mk("").digestChannels(ctx); err != nil || len(got) != 2 {
		t.Fatalf("empty channel: got %d (err=%v), want 2", len(got), err)
	}
	// Specific name (case-insensitive) → exactly that channel.
	if got, err := mk("reports").digestChannels(ctx); err != nil || len(got) != 1 || got[0].Name != "Reports" {
		t.Fatalf("named channel: got %#v (err=%v), want [Reports]", got, err)
	}
	// Configured name that doesn't match an enabled channel → none (skip send).
	if got, err := mk("Disabled").digestChannels(ctx); err != nil || len(got) != 0 {
		t.Fatalf("disabled/missing channel: got %d (err=%v), want 0", len(got), err)
	}
}

// TestSendDigestNilNotifierNoop verifies the digest is a no-op (no panic) when no
// notifier is configured — the disabled-by-default / unconfigured path.
func TestSendDigestNilNotifierNoop(t *testing.T) {
	r := &Runtime{} // notifier nil
	r.sendDigest(context.Background())
}
