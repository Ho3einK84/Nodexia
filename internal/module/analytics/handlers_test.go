package analytics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

func TestClampPct(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{-10, 0},
		{0, 0},
		{42.5, 42.5},
		{100, 100},
		{145.2, 100},
	}
	for _, tc := range cases {
		if got := clampPct(tc.in); got != tc.want {
			t.Errorf("clampPct(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestMetricLevel(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "ok"},
		{60.0, "ok"},
		{60.1, "warn"},
		{80.0, "warn"},
		{80.1, "crit"},
		{99.9, "crit"},
	}
	for _, tc := range cases {
		if got := metricLevel(tc.in); got != tc.want {
			t.Errorf("metricLevel(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRxTxSplit(t *testing.T) {
	rx, tx := rxTxSplit(0, 0)
	if rx != 0 || tx != 0 {
		t.Fatalf("rxTxSplit(0, 0) = (%d, %d), want (0, 0)", rx, tx)
	}
	rx, tx = rxTxSplit(75, 25)
	if rx != 75 || tx != 25 {
		t.Fatalf("rxTxSplit(75, 25) = (%d, %d), want (75, 25)", rx, tx)
	}
	rx, tx = rxTxSplit(100, 0)
	if rx != 100 || tx != 0 {
		t.Fatalf("rxTxSplit(100, 0) = (%d, %d), want (100, 0)", rx, tx)
	}
}

func TestGlobalAndServerAnalyticsRenderPopulatedMetricsAndTraffic(t *testing.T) {
	runtime := testutil.OpenTestDB(t)
	cfg := testutil.TestConfig(t)
	renderer, err := view.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}

	deps := module.Dependencies{
		Config:   cfg,
		Database: runtime,
		Renderer: renderer,
	}

	mux := http.NewServeMux()
	New().RegisterRoutes(mux, deps)

	ctx := context.Background()
	srvRepo := servers.NewSQLRepository(runtime.SQL)
	de, err := srvRepo.Create(ctx, servers.Server{
		Name: "Germany", Host: "203.0.113.11", Port: 22,
		AuthMode: servers.AuthModePassword, Username: "root",
		CredentialStrategy: servers.CredentialStrategyRuntime,
	})
	if err != nil {
		t.Fatalf("Create(de) = %v", err)
	}
	nl, err := srvRepo.Create(ctx, servers.Server{
		Name: "Netherlands", Host: "203.0.113.12", Port: 22,
		AuthMode: servers.AuthModePassword, Username: "root",
		CredentialStrategy: servers.CredentialStrategyRuntime,
	})
	if err != nil {
		t.Fatalf("Create(nl) = %v", err)
	}
	_ = srvRepo.UpdateCountry(ctx, de.ID, "DE", "Germany")
	_ = srvRepo.UpdateCountry(ctx, nl.ID, "NL", "Netherlands")

	if _, err := runtime.SQL.ExecContext(ctx,
		`INSERT INTO system_snapshots (server_id, cpu_usage, ram_usage, disk_usage) VALUES (?, ?, ?, ?), (?, ?, ?, ?)`,
		de.ID, 84.5, 65.0, 28.0,
		nl.ID, 12.0, 36.5, 23.0,
	); err != nil {
		t.Fatalf("insert system_snapshots: %v", err)
	}

	month := time.Now().UTC().Format("2006-01")
	const gib = int64(1024 * 1024 * 1024)
	deMonthly := `[{"label":"` + month + `","rx_bytes":` + "214748364800" + `,"tx_bytes":` + "107374182400" + `,"total_bytes":` + "322122547200" + `}]`
	nlMonthly := `[{"label":"` + month + `","rx_bytes":` + "429496729600" + `,"tx_bytes":` + "214748364800" + `,"total_bytes":` + "644245094400" + `}]`
	if _, err := runtime.SQL.ExecContext(ctx,
		`INSERT INTO vnstat_snapshots (server_id, available, monthly_rows_json) VALUES (?, 1, ?), (?, 1, ?)`,
		de.ID, deMonthly, nl.ID, nlMonthly,
	); err != nil {
		t.Fatalf("insert vnstat_snapshots: %v", err)
	}

	repo := NewSQLRepository(runtime.SQL)
	if err := repo.SetScopedLimit(ctx, LimitScopeGlobal, "", 2000*gib); err != nil {
		t.Fatalf("SetScopedLimit: %v", err)
	}

	// Global analytics view
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/analytics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /analytics = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	globalBody := rec.Body.String()
	for _, want := range []string{
		`analytics-global`,
		`an-kpi--indigo`,
		`an-avg-pills`,
		`metric-badge--crit`,
		`metric-badge--warn`,
		`metric-badge--ok`,
		`an-meter__fill--crit`,
		`an-traffic-bar__rx`,
		`an-traffic-bar__tx`,
		`traffic-badge--dl`,
		`traffic-badge--ul`,
		`an-share-pill`,
	} {
		if !strings.Contains(globalBody, want) {
			t.Errorf("GET /analytics missing %q", want)
		}
	}

	// Per-server analytics view
	recSrv := httptest.NewRecorder()
	mux.ServeHTTP(recSrv, httptest.NewRequest(http.MethodGet, "/servers/2/analytics", nil))
	if recSrv.Code != http.StatusOK {
		t.Fatalf("GET /servers/2/analytics = %d, want 200\n%s", recSrv.Code, recSrv.Body.String())
	}
	srvBody := recSrv.Body.String()
	for _, want := range []string{
		`analytics-server`,
		`chart-card--traffic`,
		`chart-card--cpu`,
		`chart-card--ram`,
		`chart-card--disk`,
		`chart-card--load`,
		`id="rangeTabIndicator"`,
		`traffic-summary__pct--dl`,
		`traffic-summary__pct--ul`,
		`traffic-summary__split-rx`,
		`an-shimmer`,
	} {
		if !strings.Contains(srvBody, want) {
			t.Errorf("GET /servers/2/analytics missing %q", want)
		}
	}

	// Limits admin view
	recLim := httptest.NewRecorder()
	mux.ServeHTTP(recLim, httptest.NewRequest(http.MethodGet, "/analytics/limits", nil))
	if recLim.Code != http.StatusOK {
		t.Fatalf("GET /analytics/limits = %d, want 200\n%s", recLim.Code, recLim.Body.String())
	}
	if !strings.Contains(recLim.Body.String(), `precedence-step--1`) {
		t.Errorf("GET /analytics/limits missing precedence-step--1")
	}
}
