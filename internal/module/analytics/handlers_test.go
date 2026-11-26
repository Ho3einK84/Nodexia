package analytics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/i18n"
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

	// Batched metrics data endpoint (1 DB scan for all 5 system series).
	recBatch := httptest.NewRecorder()
	mux.ServeHTTP(recBatch, httptest.NewRequest(http.MethodGet, "/servers/2/analytics/data?metrics=cpu,ram,disk,swap,load&range=24h", nil))
	if recBatch.Code != http.StatusOK {
		t.Fatalf("GET batched /analytics/data = %d, want 200\n%s", recBatch.Code, recBatch.Body.String())
	}
	batchBody := recBatch.Body.String()
	for _, key := range []string{`"charts"`, `"cpu"`, `"ram"`, `"disk"`, `"swap"`, `"load"`} {
		if !strings.Contains(batchBody, key) {
			t.Errorf("batched /analytics/data missing %s", key)
		}
	}

	// Inline JSON monthly limit save & clear.
	saveForm := url.Values{"limit_value": {"500"}, "limit_unit": {"GB"}, "limit_kind": {"rx"}}
	saveReq := httptest.NewRequest(http.MethodPost, "/servers/2/analytics/limit", strings.NewReader(saveForm.Encode()))
	saveReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	saveReq.Header.Set("Accept", "application/json")
	saveRec := httptest.NewRecorder()
	mux.ServeHTTP(saveRec, saveReq)
	if saveRec.Code != http.StatusOK {
		t.Fatalf("POST JSON /analytics/limit save = %d, want 200\n%s", saveRec.Code, saveRec.Body.String())
	}
	var saveResp struct {
		OK           bool   `json:"ok"`
		HasLimit     bool   `json:"has_limit"`
		BadgeVariant string `json:"badge_variant"`
		BadgeText    string `json:"badge_text"`
	}
	if err := json.Unmarshal(saveRec.Body.Bytes(), &saveResp); err != nil {
		t.Fatalf("decode limit save JSON: %v", err)
	}
	if !saveResp.OK || !saveResp.HasLimit || saveResp.BadgeVariant != "active" || saveResp.BadgeText == "" {
		t.Fatalf("unexpected limit save JSON: %+v", saveResp)
	}

	clearForm := url.Values{"limit_value": {""}}
	clearReq := httptest.NewRequest(http.MethodPost, "/servers/2/analytics/limit", strings.NewReader(clearForm.Encode()))
	clearReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	clearReq.Header.Set("Accept", "application/json")
	clearRec := httptest.NewRecorder()
	mux.ServeHTTP(clearRec, clearReq)
	if clearRec.Code != http.StatusOK {
		t.Fatalf("POST JSON /analytics/limit clear = %d, want 200\n%s", clearRec.Code, clearRec.Body.String())
	}
	var clearResp struct {
		OK       bool `json:"ok"`
		Cleared  bool `json:"cleared"`
		HasLimit bool `json:"has_limit"`
	}
	if err := json.Unmarshal(clearRec.Body.Bytes(), &clearResp); err != nil {
		t.Fatalf("decode limit clear JSON: %v", err)
	}
	if !clearResp.OK || !clearResp.Cleared || clearResp.HasLimit {
		t.Fatalf("unexpected limit clear JSON: %+v", clearResp)
	}
}

func TestAnalyticsResetDayParityRollupFallbackAndLimitsJSON(t *testing.T) {
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

	// Server 1: mid-month reset day (15th) + tag "hetzner".
	s1, err := srvRepo.Create(ctx, servers.Server{
		Name: "Anchored-DE", Host: "203.0.113.51", Port: 22,
		AuthMode: servers.AuthModePassword, Username: "root",
		CredentialStrategy: servers.CredentialStrategyRuntime,
		TrafficResetDay:    15,
		Tags:               []string{"hetzner"},
	})
	if err != nil {
		t.Fatalf("Create(s1) = %v", err)
	}

	// Server 2: registered server with NO snapshots yet (must still be counted in ServerCount).
	if _, err := srvRepo.Create(ctx, servers.Server{
		Name: "Fresh-NL", Host: "203.0.113.52", Port: 22,
		AuthMode: servers.AuthModePassword, Username: "root",
		CredentialStrategy: servers.CredentialStrategyRuntime,
		Tags:               []string{"leaseweb"},
	}); err != nil {
		t.Fatalf("Create(s2) = %v", err)
	}

	// Insert raw snapshot ONLY (no hourly rollups yet) to verify 7d/30d raw fallback.
	if _, err := runtime.SQL.ExecContext(ctx,
		`INSERT INTO system_snapshots (server_id, cpu_usage, ram_usage, swap_usage, disk_usage, load_average_1, load_average_5, load_average_15)
		 VALUES (?, 44.0, 58.0, 12.5, 31.0, 0.85, 0.72, 0.60)`,
		s1.ID,
	); err != nil {
		t.Fatalf("insert system_snapshots: %v", err)
	}

	now := time.Now().UTC()
	periodStart := TrafficPeriodStart(now, 15).Format("2006-01-02")
	const gib = int64(1024 * 1024 * 1024)
	dailyJSON := `[{"label":"` + periodStart + `","rx_bytes":107374182400,"tx_bytes":53687091200,"total_bytes":161061273600}]`
	// Deliberately put a different value in monthly_rows_json to prove GlobalHandler honours reset_day=15.
	monthlyJSON := `[{"label":"` + now.Format("2006-01") + `","rx_bytes":999999999999,"tx_bytes":999999999999,"total_bytes":1999999999998}]`
	if _, err := runtime.SQL.ExecContext(ctx,
		`INSERT INTO vnstat_snapshots (server_id, available, daily_rows_json, monthly_rows_json) VALUES (?, 1, ?, ?)`,
		s1.ID, dailyJSON, monthlyJSON,
	); err != nil {
		t.Fatalf("insert vnstat_snapshots: %v", err)
	}

	// 1) 7d and 30d data endpoints fall back to raw points when rollups table is empty, and localize in Persian.
	faCtx := i18n.NewContext(context.Background(), i18n.MustDefault().Localizer("fa"))
	dataReq := httptest.NewRequest(http.MethodGet, "/servers/1/analytics/data?metrics=cpu,ram,swap,load,traffic&range=7d", nil).WithContext(faCtx)
	dataRec := httptest.NewRecorder()
	mux.ServeHTTP(dataRec, dataReq)
	if dataRec.Code != http.StatusOK {
		t.Fatalf("GET 7d /analytics/data = %d\n%s", dataRec.Code, dataRec.Body.String())
	}
	var batchPayload struct {
		Charts map[string]ChartDataResponse `json:"charts"`
	}
	if err := json.Unmarshal(dataRec.Body.Bytes(), &batchPayload); err != nil {
		t.Fatalf("unmarshal 7d batch: %v", err)
	}
	if len(batchPayload.Charts["cpu"].Series) == 0 || len(batchPayload.Charts["cpu"].Series[0].Data) != 1 {
		t.Fatalf("expected 7d fallback to return 1 bucketed point from raw snapshot, got %+v", batchPayload.Charts["cpu"])
	}
	if batchPayload.Charts["traffic"].Series[0].Label != "دانلود" {
		t.Errorf("expected localized Persian traffic series label 'دانلود', got %q", batchPayload.Charts["traffic"].Series[0].Label)
	}

	// 2) Limits admin JSON API (tag add -> global set -> tag delete) + known fleet tags.
	tagForm := url.Values{"tag": {"hetzner"}, "limit_value": {"200"}, "limit_unit": {"GB"}}
	tagReq := httptest.NewRequest(http.MethodPost, "/analytics/limits/tags", strings.NewReader(tagForm.Encode()))
	tagReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tagReq.Header.Set("Accept", "application/json")
	tagRec := httptest.NewRecorder()
	mux.ServeHTTP(tagRec, tagReq)
	if tagRec.Code != http.StatusOK {
		t.Fatalf("POST JSON /analytics/limits/tags = %d\n%s", tagRec.Code, tagRec.Body.String())
	}
	var adminResp struct {
		OK        bool                          `json:"ok"`
		Tags      []view.TrafficLimitTagView    `json:"tags"`
		KnownTags []view.FleetTagSuggestionView `json:"known_tags"`
	}
	if err := json.Unmarshal(tagRec.Body.Bytes(), &adminResp); err != nil {
		t.Fatalf("decode admin tag JSON: %v", err)
	}
	if !adminResp.OK || len(adminResp.Tags) != 1 || adminResp.Tags[0].Tag != "hetzner" || adminResp.Tags[0].ServerCount != 1 {
		t.Fatalf("unexpected admin tag JSON: %+v", adminResp)
	}
	if len(adminResp.KnownTags) < 2 {
		t.Fatalf("expected known fleet tags (hetzner, leaseweb), got %+v", adminResp.KnownTags)
	}

	// 3) Global overview reflects real ServerCount=2, anchored 100 GiB RX + 50 GiB TX = 150 GiB total, and 50% of 200 GiB tag quota!
	gRec := httptest.NewRecorder()
	mux.ServeHTTP(gRec, httptest.NewRequest(http.MethodGet, "/analytics", nil))
	if gRec.Code != http.StatusOK {
		t.Fatalf("GET /analytics = %d", gRec.Code)
	}
	gBody := gRec.Body.String()
	for _, want := range []string{
		`>2</span>`,        // real ServerCount (including Fresh-NL without snapshots)
		`150.00 GB`,        // anchored period total (100 + 50 GiB), NOT the 1.82 TB calendar-month row
		`50% / 200.00 GB`,  // effective quota pill from "hetzner" tag cap
		`data-an-sortable`, // interactive sortable headers
	} {
		if !strings.Contains(gBody, want) {
			t.Errorf("GET /analytics missing %q", want)
		}
	}

	// 4) Per-server TX limit sets forecast header scope to Upload on SSR and returns quota telemetry in JSON.
	txForm := url.Values{"limit_value": {"100"}, "limit_unit": {"GB"}, "limit_kind": {"tx"}}
	txReq := httptest.NewRequest(http.MethodPost, "/servers/1/analytics/limit", strings.NewReader(txForm.Encode()))
	txReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	txReq.Header.Set("Accept", "application/json")
	txRec := httptest.NewRecorder()
	mux.ServeHTTP(txRec, txReq)
	if txRec.Code != http.StatusOK {
		t.Fatalf("POST TX limit = %d\n%s", txRec.Code, txRec.Body.String())
	}
	var txResp struct {
		OK            bool   `json:"ok"`
		EffectiveKind string `json:"effective_kind"`
		HasQuota      bool   `json:"has_quota"`
		QuotaPct      int    `json:"quota_pct"`
	}
	if err := json.Unmarshal(txRec.Body.Bytes(), &txResp); err != nil {
		t.Fatalf("decode TX limit JSON: %v", err)
	}
	if !txResp.OK || txResp.EffectiveKind != "tx" || !txResp.HasQuota || txResp.QuotaPct != 50 {
		t.Fatalf("expected TX limit quota_pct=50 (50 GiB of 100 GiB), got %+v", txResp)
	}

	srvPageRec := httptest.NewRecorder()
	mux.ServeHTTP(srvPageRec, httptest.NewRequest(http.MethodGet, "/servers/1/analytics", nil))
	if srvPageRec.Code != http.StatusOK {
		t.Fatalf("GET /servers/1/analytics = %d", srvPageRec.Code)
	}
	if !strings.Contains(srvPageRec.Body.String(), `<i data-lucide="arrow-up"></i> <span>Upload</span>`) {
		t.Errorf("expected SSR forecast scope badge to reflect Upload when TX limit is active")
	}
	_ = gib
}
