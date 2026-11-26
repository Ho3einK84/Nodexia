package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/scheduler"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
)

func TestSchedulerToggleReturnsJSON(t *testing.T) {
	cfg := testutil.TestConfig(t)
	dbRuntime := testutil.OpenTestDB(t)
	sshSvc := sshclient.New(cfg.SSH, cfg.Security)
	sched := scheduler.New(cfg, dbRuntime.SQL, sshSvc)
	if sched == nil {
		t.Fatal("expected non-nil scheduler runtime")
	}
	h := NewDiagnosticsHandler(cfg, dbRuntime, nil, sched, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /ops/scheduler/{serverID}/{jobType}/toggle", h.SchedulerToggle)

	// First toggle pauses the job.
	req1 := httptest.NewRequest(http.MethodPost, "/ops/scheduler/7/monitoring/toggle", nil)
	req1.Header.Set("Accept", "application/json")
	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first toggle status = %d, want 200\n%s", rec1.Code, rec1.Body.String())
	}
	var p1 struct {
		OK            bool   `json:"ok"`
		Paused        bool   `json:"paused"`
		StatusVariant string `json:"status_variant"`
		Message       string `json:"message"`
	}
	if err := json.Unmarshal(rec1.Body.Bytes(), &p1); err != nil {
		t.Fatalf("decode first toggle JSON: %v", err)
	}
	if !p1.OK || !p1.Paused || p1.StatusVariant != "idle" || p1.Message == "" {
		t.Fatalf("unexpected first toggle payload: %+v", p1)
	}

	// Second toggle resumes the job.
	req2 := httptest.NewRequest(http.MethodPost, "/ops/scheduler/7/monitoring/toggle", nil)
	req2.Header.Set("Accept", "application/json")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second toggle status = %d, want 200", rec2.Code)
	}
	var p2 struct {
		OK            bool   `json:"ok"`
		Paused        bool   `json:"paused"`
		StatusVariant string `json:"status_variant"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &p2); err != nil {
		t.Fatalf("decode second toggle JSON: %v", err)
	}
	if !p2.OK || p2.Paused || p2.StatusVariant != "ok" {
		t.Fatalf("unexpected second toggle payload: %+v", p2)
	}
}
