package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/db"
)

type HealthHandler struct {
	config   config.Config
	database *db.Runtime
}

func NewHealthHandler(cfg config.Config, database *db.Runtime) HealthHandler {
	return HealthHandler{config: cfg, database: database}
}

func (h HealthHandler) Liveness(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (h HealthHandler) Live(w http.ResponseWriter, _ *http.Request) {
	writeHealthJSON(w, http.StatusOK, healthPayload{
		Status:  "ok",
		Service: h.config.App.Name,
		Version: h.config.Version,
		Checks: map[string]checkResult{
			"process": {Status: "ok"},
		},
	})
}

func (h HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	checks := map[string]checkResult{
		"process": {Status: "ok"},
	}

	overall := "ok"
	statusCode := http.StatusOK

	if h.database == nil || h.database.SQL == nil {
		checks["database"] = checkResult{Status: "fail", Detail: "database runtime is not configured"}
		overall = "fail"
		statusCode = http.StatusServiceUnavailable
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := h.database.SQL.PingContext(ctx); err != nil {
			checks["database"] = checkResult{Status: "fail", Detail: err.Error()}
			overall = "fail"
			statusCode = http.StatusServiceUnavailable
		} else {
			checks["database"] = checkResult{Status: "ok", Detail: h.config.Database.Driver}
		}
	}

	writeHealthJSON(w, statusCode, healthPayload{
		Status:  overall,
		Service: h.config.App.Name,
		Version: h.config.Version,
		Checks:  checks,
	})
}

type healthPayload struct {
	Status  string                 `json:"status"`
	Service string                 `json:"service"`
	Version string                 `json:"version"`
	Checks  map[string]checkResult `json:"checks"`
}

type checkResult struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func writeHealthJSON(w http.ResponseWriter, statusCode int, payload healthPayload) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}
