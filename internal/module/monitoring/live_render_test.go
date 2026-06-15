package monitoring_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/testutil"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

// TestMonitoringPageRendersLivePanel executes the content-monitoring template
// end to end so a broken field reference in the live panel is caught at test
// time rather than in production. It checks both the enabled (WebSocket) and
// disabled paths.
func TestMonitoringPageRendersLivePanel(t *testing.T) {
	cfg := testutil.TestConfig(t)
	renderer, err := view.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	basePage := func() view.PageData {
		pd := view.NewPageData(cfg, httptest.NewRequest(http.MethodGet, "/", nil))
		pd.ContentTemplate = "content-monitoring"
		pd.Title = "Monitoring"
		pd.MonitoringTarget = view.MonitoringTargetView{ID: 5, Name: "lab", Host: "10.0.0.1", Port: 22}
		return pd
	}

	t.Run("live enabled", func(t *testing.T) {
		pd := basePage()
		pd.MonitoringLive = view.MonitoringLiveView{
			Enabled:         true,
			WSURL:           "/servers/5/monitoring/live",
			IntervalSeconds: 3,
		}
		rec := httptest.NewRecorder()
		if err := renderer.Render(rec, http.StatusOK, pd); err != nil {
			t.Fatalf("render: %v", err)
		}
		body := rec.Body.String()
		// The unified card carries the live WebSocket hook, the live status pill,
		// and live-driven gauges (even with no stored snapshot).
		if !strings.Contains(body, `data-live-url="/servers/5/monitoring/live"`) {
			t.Error("expected the live WebSocket URL data attribute")
		}
		if !strings.Contains(body, "data-live-status") {
			t.Error("expected the live status pill")
		}
		if !strings.Contains(body, `data-gauge-metric="cpu"`) {
			t.Error("expected the live-driven CPU gauge")
		}
	})

	t.Run("live disabled", func(t *testing.T) {
		pd := basePage() // MonitoringLive zero value → Enabled false
		rec := httptest.NewRecorder()
		if err := renderer.Render(rec, http.StatusOK, pd); err != nil {
			t.Fatalf("render: %v", err)
		}
		if strings.Contains(rec.Body.String(), "data-live-url") {
			t.Error("live panel must be absent when not enabled")
		}
	})
}
