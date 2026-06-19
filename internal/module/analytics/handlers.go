package analytics

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/geoip"
	"github.com/Ho3einK84/Nodexia/internal/http/httperrors"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

// ── Chart data types ──────────────────────────────────────────────────────────

type ChartSeries struct {
	Label string    `json:"label"`
	Color string    `json:"color"`
	Fill  string    `json:"fill,omitempty"`
	Data  []float64 `json:"data"`
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

	page := view.NewPageData(h.deps.Config, r)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = page.T("nav.analytics")
	page.ActiveNav = "/analytics"
	page.ContentTemplate = "content-analytics"
	page.PageTitle = page.T("analytics.page_title", "server", server.Name)
	page.SetServerCountry(server.CountryCode, server.CountryName)
	page.PageDescription = page.T("analytics.page_description")
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
	page.AnalyticsTrafficMonth = h.currentMonthTraffic(r, server.ID)
	page.AnalyticsLimit = h.limitView(r, server.ID)
	if kind, msg := limitFlash(r, page); kind != "" {
		page.FlashKind = kind
		page.FlashMessage = msg
	}
	page.PageStyles = []string{"/static/analytics.css"}
	page.PageScripts = []string{"/static/analytics.js"}

	if err := h.deps.Renderer.Render(w, http.StatusOK, page); err != nil {
		http.Error(w, "render analytics page", http.StatusInternalServerError)
	}
}

// limitView builds the monthly download-limit form state for a server. On any
// read error it renders the form as "no limit" rather than failing the page —
// the limit is an optional convenience, never a hard dependency of analytics.
func (h PageHandler) limitView(r *http.Request, serverID int64) view.AnalyticsLimitView {
	v := view.AnalyticsLimitView{
		Action:      fmt.Sprintf("/servers/%d/analytics/limit", serverID),
		UnitInput:   defaultLimitUnit,
		UnitOptions: limitUnitOptions,
	}
	limitBytes, ok, err := h.repo.GetTrafficLimit(r.Context(), serverID)
	if err != nil || !ok {
		return v
	}
	v.HasLimit = true
	v.LimitHuman = formatBytes(limitBytes)
	v.ValueInput, v.UnitInput = limitToValueUnit(limitBytes)
	return v
}

// limitFlash maps the ?flash= marker set after a limit POST to a kind + message.
// page.T resolves the key in the request's active language.
func limitFlash(r *http.Request, page view.PageData) (kind, message string) {
	switch r.URL.Query().Get("flash") {
	case "limit-saved":
		return "success", page.T("analytics.limit.flash_saved")
	case "limit-cleared":
		return "success", page.T("analytics.limit.flash_cleared")
	default:
		return "", ""
	}
}

// currentMonthTraffic reads the server's latest vnstat snapshot and pulls out
// the current month's download/upload/total. Returns HasData=false (rendered as
// an empty state) on any error or when the current month is missing, so the
// page never fails just because traffic hasn't been collected yet.
func (h PageHandler) currentMonthTraffic(r *http.Request, serverID int64) view.AnalyticsTrafficSummaryView {
	currentMonth := time.Now().UTC().Format("2006-01")
	summary := view.AnalyticsTrafficSummaryView{MonthLabel: currentMonth}

	_, months, err := h.repo.GetLatestTrafficForServer(r.Context(), serverID)
	if err != nil {
		return summary
	}
	for _, m := range months {
		if m.Label != currentMonth {
			continue
		}
		total := m.Total
		if total == 0 {
			total = m.RX + m.TX
		}
		summary.HasData = true
		summary.Download = formatBytes(m.RX)
		summary.Upload = formatBytes(m.TX)
		summary.Total = formatBytes(total)
		break
	}
	return summary
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
			ServerID:    s.ServerID,
			ServerName:  s.ServerName,
			FlagEmoji:   geoip.FlagEmoji(s.CountryCode),
			CountryName: geoip.CountryName(s.CountryCode),
			CPU:         fmt.Sprintf("%.1f%%", s.AvgCPU),
			RAM:         fmt.Sprintf("%.1f%%", s.AvgRAM),
			Disk:        fmt.Sprintf("%.1f%%", s.AvgDisk),
		})
	}

	// Sort traffic descending by monthly total, then keep the top 10 (the repo
	// returns every server because the total can't be sorted in SQL).
	sort.Slice(topTraffic, func(i, j int) bool {
		return topTraffic[i].MonthBytes > topTraffic[j].MonthBytes
	})
	if len(topTraffic) > 10 {
		topTraffic = topTraffic[:10]
	}
	trafficViews := make([]view.TopServerTrafficView, 0, len(topTraffic))
	for _, s := range topTraffic {
		trafficViews = append(trafficViews, view.TopServerTrafficView{
			ServerID:    s.ServerID,
			ServerName:  s.ServerName,
			FlagEmoji:   geoip.FlagEmoji(s.CountryCode),
			CountryName: geoip.CountryName(s.CountryCode),
			Download:    formatBytes(s.MonthRX),
			Upload:      formatBytes(s.MonthTX),
			MonthBytes:  formatBytes(s.MonthBytes),
			MonthLabel:  s.MonthLabel,
		})
	}

	page := view.NewPageData(h.deps.Config, r)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = page.T("nav.analytics")
	page.ActiveNav = "/analytics"
	page.ContentTemplate = "content-analytics-global"
	page.PageTitle = page.T("analytics.global_title")
	page.PageDescription = page.T("analytics.global_description")
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

	// A missing/failed limit lookup must never break the forecast — treat it as
	// "no limit" so the response is identical to a server without a cap.
	limitBytes, _, _ := h.repo.GetTrafficLimit(r.Context(), server.ID)

	out := h.forecastSvc.Compute(days, months, limitBytes)
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

// ── Traffic-limit form handler ──────────────────────────────────────────────

// limitUnitOptions are the units the limit form accepts. GiB and TiB cover the
// realistic range of VPS monthly download caps; defaultLimitUnit pre-selects the
// most common one for a fresh form.
var limitUnitOptions = []string{"GiB", "TiB"}

const defaultLimitUnit = "GiB"

type LimitHandler struct {
	deps       module.Dependencies
	serverRepo servers.Repository
	repo       Repository
}

func NewLimitHandler(deps module.Dependencies, serverRepo servers.Repository, repo Repository) LimitHandler {
	return LimitHandler{deps: deps, serverRepo: serverRepo, repo: repo}
}

// ServeHTTP handles POST of the monthly download-limit form. An empty value
// clears the limit; a positive value sets it; zero, negative, or unparseable
// input is rejected and the page is re-rendered with an inline error. CSRF is
// enforced by the global middleware before this handler runs.
func (h LimitHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	server, ok := loadServer(w, r, h.deps, h.serverRepo)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, server, "", defaultLimitUnit, "analytics.limit.error_invalid")
		return
	}

	rawValue := strings.TrimSpace(r.FormValue("limit_value"))
	unit := strings.TrimSpace(r.FormValue("limit_unit"))
	if !validLimitUnit(unit) {
		unit = defaultLimitUnit
	}

	// Empty value means "clear the limit" — a first-class action, not an error.
	if rawValue == "" {
		if err := h.repo.DeleteTrafficLimit(r.Context(), server.ID); err != nil {
			h.renderError(w, r, server, rawValue, unit, "analytics.limit.error_save")
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/servers/%d/analytics?flash=limit-cleared", server.ID), http.StatusSeeOther)
		return
	}

	limitBytes, ok := parseLimitBytes(rawValue, unit)
	if !ok {
		h.renderError(w, r, server, rawValue, unit, "analytics.limit.error_positive")
		return
	}

	if err := h.repo.SetTrafficLimit(r.Context(), server.ID, limitBytes); err != nil {
		h.renderError(w, r, server, rawValue, unit, "analytics.limit.error_save")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/servers/%d/analytics?flash=limit-saved", server.ID), http.StatusSeeOther)
}

// renderError re-renders the analytics page with the limit form showing an
// inline validation error and the user's rejected input preserved.
func (h LimitHandler) renderError(w http.ResponseWriter, r *http.Request, server servers.Server, value, unit, errKey string) {
	page := view.NewPageData(h.deps.Config, r)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = page.T("nav.analytics")
	page.ActiveNav = "/analytics"
	page.ContentTemplate = "content-analytics"
	page.PageTitle = page.T("analytics.page_title", "server", server.Name)
	page.SetServerCountry(server.CountryCode, server.CountryName)
	page.PageDescription = page.T("analytics.page_description")
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

	pageHandler := PageHandler{deps: h.deps, serverRepo: h.serverRepo, repo: h.repo}
	page.AnalyticsTrafficMonth = pageHandler.currentMonthTraffic(r, server.ID)
	limitView := pageHandler.limitView(r, server.ID)
	limitView.ValueInput = value
	limitView.UnitInput = unit
	limitView.Error = page.T(errKey)
	page.AnalyticsLimit = limitView
	page.FlashKind = "error"
	page.FlashMessage = page.T(errKey)
	page.PageStyles = []string{"/static/analytics.css"}
	page.PageScripts = []string{"/static/analytics.js"}

	if err := h.deps.Renderer.Render(w, http.StatusUnprocessableEntity, page); err != nil {
		http.Error(w, "render analytics page", http.StatusInternalServerError)
	}
}

func validLimitUnit(unit string) bool {
	for _, u := range limitUnitOptions {
		if u == unit {
			return true
		}
	}
	return false
}

// parseLimitBytes converts a value + unit into a byte count. It returns ok=false
// for anything that is not a strictly-positive, finite number, so zero and
// negative limits are rejected (an unset limit is expressed by clearing, not by
// storing zero).
func parseLimitBytes(rawValue, unit string) (int64, bool) {
	value, err := strconv.ParseFloat(rawValue, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return 0, false
	}
	var mult float64
	switch unit {
	case "TiB":
		mult = 1024 * 1024 * 1024 * 1024
	default: // GiB
		mult = 1024 * 1024 * 1024
	}
	bytes := int64(value * mult)
	if bytes <= 0 {
		return 0, false
	}
	return bytes, true
}

// limitToValueUnit renders a stored byte count back into a form-friendly value +
// unit, preferring TiB once the cap reaches 1 TiB so large plans read cleanly.
func limitToValueUnit(bytes int64) (value, unit string) {
	const tib = float64(1024 * 1024 * 1024 * 1024)
	const gib = float64(1024 * 1024 * 1024)
	if float64(bytes) >= tib {
		return trimFloat(float64(bytes) / tib), "TiB"
	}
	return trimFloat(float64(bytes) / gib), "GiB"
}

// trimFloat formats a float with up to 2 decimals and no trailing zeros, so a
// whole-number limit shows as "500" rather than "500.00".
func trimFloat(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
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
