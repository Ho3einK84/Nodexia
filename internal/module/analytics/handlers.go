package analytics

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/http/httperrors"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

// ── Chart data types ──────────────────────────────────────────────────────────

type ChartSeries struct {
	Label  string    `json:"label"`
	Color  string    `json:"color"`
	Fill   string    `json:"fill,omitempty"`
	Data   []float64 `json:"data"`
}

type ChartDataResponse struct {
	Metric string        `json:"metric"`
	Range  string        `json:"range"`
	Unit   string        `json:"unit"`
	Labels []string      `json:"labels"`
	Series []ChartSeries `json:"series"`
	Min    float64       `json:"min"`
	Max    float64       `json:"max"`
}

// ── Forecast data types ───────────────────────────────────────────────────────

type PeriodForecastJSON struct {
	CurrentBytes   int64  `json:"current_bytes"`
	PredictedBytes int64  `json:"predicted_bytes"`
	CurrentHuman   string `json:"current_human"`
	PredictedHuman string `json:"predicted_human"`
	PctElapsed     int    `json:"pct_elapsed"`
}

type ForecastRisksJSON struct {
	Exhaustion    bool `json:"exhaustion"`
	TrafficSpike  bool `json:"traffic_spike"`
	UnusualGrowth bool `json:"unusual_growth"`
}

type ForecastResponseJSON struct {
	Today      PeriodForecastJSON `json:"today"`
	ThisWeek   PeriodForecastJSON `json:"this_week"`
	ThisMonth  PeriodForecastJSON `json:"this_month"`
	Algorithm  string             `json:"algorithm"`
	Confidence string             `json:"confidence"`
	Trend      string             `json:"trend"`
	Risks      ForecastRisksJSON  `json:"risks"`
}

// ── Page handler ──────────────────────────────────────────────────────────────

type PageHandler struct {
	deps       module.Dependencies
	serverRepo servers.Repository
	repo       Repository
}

func NewPageHandler(deps module.Dependencies, serverRepo servers.Repository, repo Repository) PageHandler {
	return PageHandler{deps: deps, serverRepo: serverRepo, repo: repo}
}

func (h PageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	server, ok := loadServer(w, r, h.deps, h.serverRepo)
	if !ok {
		return
	}

	page := view.NewPageData(h.deps.Config)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = "Analytics"
	page.ActiveNav = "/analytics"
	page.ContentTemplate = "content-analytics"
	page.PageTitle = "Analytics for " + server.Name
	page.PageDescription = "Historical metrics, trends, and bandwidth forecasting."
	if h.deps.Database != nil {
		page.MigrationCount = h.deps.Database.MigrationCount()
	}
	page.AnalyticsTarget = view.AnalyticsTargetView{
		ID:                 server.ID,
		Name:               server.Name,
		Host:               server.Host,
		Port:               server.Port,
		AuthMode:           server.AuthMode,
		Username:           server.Username,
		Tags:               server.Tags,
		CredentialStrategy: server.CredentialStrategy,
	}
	page.PageStyles = []string{"/static/analytics.css"}
	page.PageScripts = []string{"/static/analytics.js"}

	if err := h.deps.Renderer.Render(w, http.StatusOK, page); err != nil {
		http.Error(w, "render analytics page", http.StatusInternalServerError)
	}
}

// ── Global overview handler ───────────────────────────────────────────────────

type GlobalHandler struct {
	deps module.Dependencies
	repo Repository
}

func NewGlobalHandler(deps module.Dependencies, repo Repository) GlobalHandler {
	return GlobalHandler{deps: deps, repo: repo}
}

func (h GlobalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	topMetrics, _ := h.repo.ListServerMetricSummaries(r.Context(), 10)
	topTraffic, _ := h.repo.ListServerTrafficSummaries(r.Context(), 10)

	metricViews := make([]view.TopServerMetricView, 0, len(topMetrics))
	for _, s := range topMetrics {
		metricViews = append(metricViews, view.TopServerMetricView{
			ServerID:   s.ServerID,
			ServerName: s.ServerName,
			CPU:        fmt.Sprintf("%.1f%%", s.AvgCPU),
			RAM:        fmt.Sprintf("%.1f%%", s.AvgRAM),
			Disk:       fmt.Sprintf("%.1f%%", s.AvgDisk),
		})
	}

	// Sort traffic descending by monthly bytes.
	sort.Slice(topTraffic, func(i, j int) bool {
		return topTraffic[i].MonthBytes > topTraffic[j].MonthBytes
	})
	trafficViews := make([]view.TopServerTrafficView, 0, len(topTraffic))
	for _, s := range topTraffic {
		trafficViews = append(trafficViews, view.TopServerTrafficView{
			ServerID:    s.ServerID,
			ServerName:  s.ServerName,
			MonthBytes:  formatBytes(s.MonthBytes),
			MonthLabel:  s.MonthLabel,
		})
	}

	page := view.NewPageData(h.deps.Config)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = "Analytics"
	page.ActiveNav = "/analytics"
	page.ContentTemplate = "content-analytics-global"
	page.PageTitle = "Analytics Overview"
	page.PageDescription = "Global resource usage and bandwidth consumption across all servers."
	if h.deps.Database != nil {
		page.MigrationCount = h.deps.Database.MigrationCount()
	}
	page.GlobalAnalytics = view.GlobalAnalyticsView{
		TopMetrics:  metricViews,
		TopTraffic:  trafficViews,
		ServerCount: len(topMetrics),
	}
	page.PageStyles = []string{"/static/analytics.css"}
	page.PageScripts = []string{"/static/analytics.js"}

	if err := h.deps.Renderer.Render(w, http.StatusOK, page); err != nil {
		http.Error(w, "render global analytics page", http.StatusInternalServerError)
	}
}

// ── Chart data API handler ────────────────────────────────────────────────────

type DataHandler struct {
	deps       module.Dependencies
	serverRepo servers.Repository
	repo       Repository
}

func NewDataHandler(deps module.Dependencies, serverRepo servers.Repository, repo Repository) DataHandler {
	return DataHandler{deps: deps, serverRepo: serverRepo, repo: repo}
}

func (h DataHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	server, ok := loadServer(w, r, h.deps, h.serverRepo)
	if !ok {
		return
	}

	metric := strings.TrimSpace(r.URL.Query().Get("metric"))
	rangeStr := strings.TrimSpace(r.URL.Query().Get("range"))

	since, rangeLabel, err := parseRange(rangeStr)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid range parameter")
		return
	}

	var resp ChartDataResponse
	resp.Range = rangeLabel

	switch metric {
	case "cpu", "ram", "disk", "swap":
		resp, err = h.buildSystemChart(r, server.ID, metric, since, rangeLabel)
	case "load":
		resp, err = h.buildLoadChart(r, server.ID, since, rangeLabel)
	case "traffic":
		resp, err = h.buildTrafficChart(r, server.ID)
	default:
		writeJSONError(w, http.StatusBadRequest, "unknown metric; use cpu, ram, disk, swap, load, or traffic")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load chart data")
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h DataHandler) buildSystemChart(r *http.Request, serverID int64, metric string, since time.Time, rangeLabel string) (ChartDataResponse, error) {
	var labels []string
	var values []float64

	// For short ranges use raw data; for 7d+ use hourly rollups.
	if rangeLabel == "7d" || rangeLabel == "30d" {
		rollups, err := h.repo.ListHourlyRollups(r.Context(), serverID, since)
		if err != nil {
			return ChartDataResponse{}, err
		}
		for _, rp := range rollups {
			labels = append(labels, rp.PeriodStart.Format("01/02 15:04"))
			values = append(values, extractHourlyMetric(rp, metric))
		}
	} else {
		points, err := h.repo.ListRawSince(r.Context(), serverID, since)
		if err != nil {
			return ChartDataResponse{}, err
		}
		for _, p := range points {
			labels = append(labels, formatLabel(p.RecordedAt, rangeLabel))
			values = append(values, extractRawMetric(p, metric))
		}
	}

	color, label := metricStyle(metric)
	return ChartDataResponse{
		Metric: metric,
		Range:  rangeLabel,
		Unit:   "%",
		Labels: labels,
		Series: []ChartSeries{{Label: label, Color: color, Fill: color + "22", Data: values}},
		Min:    0,
		Max:    100,
	}, nil
}

func (h DataHandler) buildLoadChart(r *http.Request, serverID int64, since time.Time, rangeLabel string) (ChartDataResponse, error) {
	var labels []string
	var load1, load5, load15 []float64

	if rangeLabel == "7d" || rangeLabel == "30d" {
		rollups, err := h.repo.ListHourlyRollups(r.Context(), serverID, since)
		if err != nil {
			return ChartDataResponse{}, err
		}
		for _, rp := range rollups {
			labels = append(labels, rp.PeriodStart.Format("01/02 15:04"))
			load1 = append(load1, rp.AvgLoad1)
			load5 = append(load5, rp.AvgLoad5)
			load15 = append(load15, rp.AvgLoad15)
		}
	} else {
		points, err := h.repo.ListRawSince(r.Context(), serverID, since)
		if err != nil {
			return ChartDataResponse{}, err
		}
		for _, p := range points {
			labels = append(labels, formatLabel(p.RecordedAt, rangeLabel))
			load1 = append(load1, p.LoadAvg1)
			load5 = append(load5, p.LoadAvg5)
			load15 = append(load15, p.LoadAvg15)
		}
	}

	return ChartDataResponse{
		Metric: "load",
		Range:  rangeLabel,
		Unit:   "",
		Labels: labels,
		Series: []ChartSeries{
			{Label: "Load 1m", Color: "#3b82f6", Data: load1},
			{Label: "Load 5m", Color: "#8b5cf6", Data: load5},
			{Label: "Load 15m", Color: "#64748b", Data: load15},
		},
		Min: 0,
		Max: autoMax(load1, load5, load15),
	}, nil
}

func (h DataHandler) buildTrafficChart(r *http.Request, serverID int64) (ChartDataResponse, error) {
	days, _, err := h.repo.GetLatestTrafficForServer(r.Context(), serverID)
	if err != nil {
		return ChartDataResponse{}, err
	}

	// Sort chronologically, keep last 30 days.
	sort.Slice(days, func(i, j int) bool { return days[i].Label < days[j].Label })
	if len(days) > 30 {
		days = days[len(days)-30:]
	}

	labels := make([]string, 0, len(days))
	rxData := make([]float64, 0, len(days))
	txData := make([]float64, 0, len(days))

	const gib = float64(1024 * 1024 * 1024)
	var maxVal float64
	for _, d := range days {
		labels = append(labels, d.Label[5:]) // strip year → "01-15"
		rxGiB := float64(d.RX) / gib
		txGiB := float64(d.TX) / gib
		rxData = append(rxData, rxGiB)
		txData = append(txData, txGiB)
		if rxGiB > maxVal {
			maxVal = rxGiB
		}
		if txGiB > maxVal {
			maxVal = txGiB
		}
	}

	return ChartDataResponse{
		Metric: "traffic",
		Range:  "30d",
		Unit:   "GiB",
		Labels: labels,
		Series: []ChartSeries{
			{Label: "Download", Color: "#3b82f6", Fill: "#3b82f622", Data: rxData},
			{Label: "Upload", Color: "#8b5cf6", Fill: "#8b5cf622", Data: txData},
		},
		Min: 0,
		Max: ceilToNice(maxVal),
	}, nil
}

// ── Forecast API handler ──────────────────────────────────────────────────────

type ForecastHandler struct {
	deps        module.Dependencies
	serverRepo  servers.Repository
	repo        Repository
	forecastSvc *ForecastService
}

func NewForecastHandler(deps module.Dependencies, serverRepo servers.Repository, repo Repository, svc *ForecastService) ForecastHandler {
	return ForecastHandler{deps: deps, serverRepo: serverRepo, repo: repo, forecastSvc: svc}
}

func (h ForecastHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	server, ok := loadServer(w, r, h.deps, h.serverRepo)
	if !ok {
		return
	}

	days, months, err := h.repo.GetLatestTrafficForServer(r.Context(), server.ID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to load traffic data")
		return
	}

	out := h.forecastSvc.Compute(days, months)
	now := time.Now().UTC()

	todayPctElapsed := int((float64(now.Hour()*60+now.Minute()) / float64(24*60)) * 100)
	weekdayOffset := int(now.Weekday())
	if weekdayOffset == 0 {
		weekdayOffset = 7
	}
	weekPctElapsed := int((float64(weekdayOffset-1)*24*60 + float64(now.Hour()*60+now.Minute())) / float64(7*24*60) * 100)
	monthPctElapsed := int((float64(now.Day()-1)*24*60 + float64(now.Hour()*60+now.Minute())) / float64(daysInMonth(now.Year(), now.Month())*24*60) * 100)

	resp := ForecastResponseJSON{
		Today: PeriodForecastJSON{
			CurrentBytes:   out.Today.CurrentBytes,
			PredictedBytes: out.Today.PredictedBytes,
			CurrentHuman:   formatBytes(out.Today.CurrentBytes),
			PredictedHuman: formatBytes(out.Today.PredictedBytes),
			PctElapsed:     todayPctElapsed,
		},
		ThisWeek: PeriodForecastJSON{
			CurrentBytes:   out.ThisWeek.CurrentBytes,
			PredictedBytes: out.ThisWeek.PredictedBytes,
			CurrentHuman:   formatBytes(out.ThisWeek.CurrentBytes),
			PredictedHuman: formatBytes(out.ThisWeek.PredictedBytes),
			PctElapsed:     weekPctElapsed,
		},
		ThisMonth: PeriodForecastJSON{
			CurrentBytes:   out.ThisMonth.CurrentBytes,
			PredictedBytes: out.ThisMonth.PredictedBytes,
			CurrentHuman:   formatBytes(out.ThisMonth.CurrentBytes),
			PredictedHuman: formatBytes(out.ThisMonth.PredictedBytes),
			PctElapsed:     monthPctElapsed,
		},
		Algorithm:  out.Algorithm,
		Confidence: string(out.Confidence),
		Trend:      string(out.Trend),
		Risks: ForecastRisksJSON{
			Exhaustion:    out.Risks.Exhaustion,
			TrafficSpike:  out.Risks.TrafficSpike,
			UnusualGrowth: out.Risks.UnusualGrowth,
		},
	}

	writeJSON(w, http.StatusOK, resp)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func loadServer(w http.ResponseWriter, r *http.Request, deps module.Dependencies, serverRepo servers.Repository) (servers.Server, bool) {
	rawID := r.PathValue("id")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id < 1 {
		httperrors.RenderPage(w, r, deps, servers.ErrNotFound, "/servers", "Server not found", "The requested server does not exist.")
		return servers.Server{}, false
	}
	server, err := serverRepo.GetByID(r.Context(), id)
	if err != nil {
		httperrors.RenderPage(w, r, deps, err, "/servers", "Could not load server", "The analytics page could not load the selected server.")
		return servers.Server{}, false
	}
	return server, true
}

func parseRange(rangeStr string) (time.Time, string, error) {
	now := time.Now().UTC()
	switch rangeStr {
	case "", "24h":
		return now.Add(-24 * time.Hour), "24h", nil
	case "1h":
		return now.Add(-time.Hour), "1h", nil
	case "6h":
		return now.Add(-6 * time.Hour), "6h", nil
	case "7d":
		return now.Add(-7 * 24 * time.Hour), "7d", nil
	case "30d":
		return now.Add(-30 * 24 * time.Hour), "30d", nil
	default:
		return time.Time{}, "", fmt.Errorf("unknown range %q", rangeStr)
	}
}

func formatLabel(t time.Time, rangeLabel string) string {
	switch rangeLabel {
	case "1h":
		return t.Format("15:04")
	case "6h", "24h":
		return t.Format("15:04")
	default:
		return t.Format("01/02 15:04")
	}
}

func extractRawMetric(p RawPoint, metric string) float64 {
	switch metric {
	case "cpu":
		return p.CPUUsage
	case "ram":
		return p.RAMUsage
	case "disk":
		return p.DiskUsage
	case "swap":
		return p.SwapUsage
	default:
		return 0
	}
}

func extractHourlyMetric(rp HourlyRollup, metric string) float64 {
	switch metric {
	case "cpu":
		return rp.AvgCPU
	case "ram":
		return rp.AvgRAM
	case "disk":
		return rp.AvgDisk
	case "swap":
		return rp.AvgSwap
	default:
		return 0
	}
}

func metricStyle(metric string) (color, label string) {
	switch metric {
	case "cpu":
		return "#3b82f6", "CPU %"
	case "ram":
		return "#8b5cf6", "RAM %"
	case "disk":
		return "#f59e0b", "Disk %"
	case "swap":
		return "#ec4899", "Swap %"
	default:
		return "#64748b", metric
	}
}

func autoMax(series ...[]float64) float64 {
	var m float64 = 1
	for _, s := range series {
		for _, v := range s {
			if v > m {
				m = v
			}
		}
	}
	return ceilToNice(m)
}

func ceilToNice(v float64) float64 {
	if v <= 0 {
		return 1
	}
	// Round up to nearest "nice" number.
	for _, nice := range []float64{0.5, 1, 2, 5, 10, 25, 50, 100, 200, 500, 1000, 5000, 10000} {
		if v <= nice {
			return nice
		}
	}
	return v * 1.2
}

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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
