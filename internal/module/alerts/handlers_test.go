package alerts_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/alerts"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

// newAlertsMux wires the alerts module against a real database and renderer so
// the routing, handlers, view builders, and templates are all exercised.
func newAlertsMux(t *testing.T) (*http.ServeMux, *alerts.SQLRepository, int64) {
	t.Helper()

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
	alerts.New().RegisterRoutes(mux, deps)

	ctx := context.Background()
	serverID := newTestServer(t, ctx, runtime, "lab-1")
	repo := alerts.NewSQLRepository(runtime.SQL)
	if _, err := repo.CreateChannel(ctx, alerts.Channel{Kind: alerts.ChannelKindTelegram, Name: "Ops", ChatID: "-100", Enabled: true}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if _, err := repo.CreateRule(ctx, alerts.Rule{ServerID: ptr(serverID), Metric: alerts.MetricCPU, Threshold: 90, Enabled: true, Note: "seed"}); err != nil {
		t.Fatalf("seed rule: %v", err)
	}
	if _, err := repo.CreateSilence(ctx, alerts.Silence{ServerID: serverID, Metric: alerts.MetricDisk, Reason: "seed"}); err != nil {
		t.Fatalf("seed silence: %v", err)
	}

	return mux, &repo, serverID
}

func TestAlertsGetPagesRender(t *testing.T) {
	mux, _, _ := newAlertsMux(t)

	paths := []string{
		"/alerts",
		"/alerts/rules/new",
		"/alerts/rules/1/edit",
		"/alerts/channels/new",
		"/alerts/channels/1/edit",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200\n%s", path, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAlertsCreateRuleRedirects(t *testing.T) {
	mux, repo, serverID := newAlertsMux(t)

	form := url.Values{}
	form.Set("server_id", strconv.FormatInt(serverID, 10))
	form.Set("metric", alerts.MetricRAM)
	form.Set("comparator", alerts.ComparatorGTE)
	form.Set("threshold", "85")
	form.Set("consecutive_hits", "2")
	form.Set("cooldown_seconds", "300")
	form.Set("severity", alerts.SeverityCritical)
	form.Set("enabled", "on")

	req := httptest.NewRequest(http.MethodPost, "/alerts/rules", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /alerts/rules = %d, want 303\n%s", rec.Code, rec.Body.String())
	}

	rules, err := repo.ListRules(context.Background())
	if err != nil {
		t.Fatalf("ListRules() error = %v", err)
	}
	// One seeded rule + the one just created.
	if len(rules) != 2 {
		t.Fatalf("ListRules() len = %d, want 2", len(rules))
	}
}

func TestAlertsCreateRuleRejectsBadInput(t *testing.T) {
	mux, _, _ := newAlertsMux(t)

	form := url.Values{}
	form.Set("metric", "bogus")
	form.Set("threshold", "")

	req := httptest.NewRequest(http.MethodPost, "/alerts/rules", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /alerts/rules (bad) = %d, want 422\n%s", rec.Code, rec.Body.String())
	}
}

func TestAlertsServerSilenceRedirects(t *testing.T) {
	mux, repo, serverID := newAlertsMux(t)

	form := url.Values{}
	form.Set("metric", alerts.MetricCPU)

	req := httptest.NewRequest(http.MethodPost, "/servers/"+strconv.FormatInt(serverID, 10)+"/alerts/silence", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST server silence = %d, want 303\n%s", rec.Code, rec.Body.String())
	}

	silenced, err := repo.IsSilenced(context.Background(), serverID, alerts.MetricCPU)
	if err != nil {
		t.Fatalf("IsSilenced() error = %v", err)
	}
	if !silenced {
		t.Fatal("expected cpu silenced for server after one-click silence")
	}
}
