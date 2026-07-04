package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/config"
)

func metricsConfig(token string) config.Config {
	cfg := config.Config{Version: "test"}
	cfg.Security.MetricsToken = token
	return cfg
}

// TestMetricsDisabledWithoutToken keeps the endpoint invisible unless the
// operator explicitly configures a scrape token.
func TestMetricsDisabledWithoutToken(t *testing.T) {
	h := NewMetricsHandler(metricsConfig(""), nil, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404 when no token is configured", rec.Code)
	}
}

func TestMetricsRejectsBadToken(t *testing.T) {
	h := NewMetricsHandler(metricsConfig("s3cret"), nil, nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 401 {
		t.Fatalf("no token: status = %d, want 401", rec.Code)
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("wrong token: status = %d, want 401", rec.Code)
	}
}

func TestMetricsServesExposition(t *testing.T) {
	h := NewMetricsHandler(metricsConfig("s3cret"), nil, nil)

	// Bearer header.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `nodexia_build_info{version="test"} 1`) {
		t.Fatalf("missing build info in body:\n%s", body)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain exposition", got)
	}

	// Query-parameter fallback for scrapers that cannot set headers.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics?token=s3cret", nil))
	if rec.Code != 200 {
		t.Fatalf("query token: status = %d, want 200", rec.Code)
	}
}
