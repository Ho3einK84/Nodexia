package analytics

import (
	"context"
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
	"github.com/Ho3einK84/Nodexia/internal/humanize"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

// fleetMetaRepo exposes optional batch fleet helpers implemented by SQLRepository.
type fleetMetaRepo interface {
	CountServers(ctx context.Context) (int, error)
	LoadLimitResolver(ctx context.Context) (*LimitResolver, error)
	ListServerTagsMap(ctx context.Context) (map[int64][]string, error)
}

// ── Chart data types ──────────────────────────────────────────────────────────

type ChartSeries struct {
	Metric string    `json:"metric,omitempty"`
	Label  string    `json:"label"`
	Color  string    `json:"color,omitempty"`
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

// ExhaustionForecastJSON is the days-to-limit projection for the JSON response.
// has_limit is false for servers without a configured cap, in which case the UI
// omits the panel entirely.
type ExhaustionForecastJSON struct {
	HasLimit          bool   `json:"has_limit"`
	LimitBytes        int64  `json:"limit_bytes"`
	LimitHuman        string `json:"limit_human"`
	AlreadyOver       bool   `json:"already_over"`
	WillExhaust       bool   `json:"will_exhaust"`
	DaysRemaining     int    `json:"days_remaining"`
	ExhaustionDate    string `json:"exhaustion_date"`
	DaysUntilMonthEnd int    `json:"days_until_month_end"`
	ProjectedBytes    int64  `json:"projected_month_bytes"`
	ProjectedHuman    string `json:"projected_month_human"`
}

type ForecastResponseJSON struct {
	Today      PeriodForecastJSON `json:"today"`
	ThisWeek   PeriodForecastJSON `json:"this_week"`
	ThisMonth  PeriodForecastJSON `json:"this_month"`
	Algorithm  string             `json:"algorithm"`
	Confidence string             `json:"confidence"`
	Trend      string             `json:"trend"`
	// Series is the traffic series the forecast (and any limit) measures:
	// "rx", "tx", or "total". PeriodStart/PeriodEnd bound the accounting
	// period ("2006-01-02", end exclusive) — the calendar month unless the
	// server has a billing-cycle anchor configured.
	Series      string                 `json:"series"`
	PeriodStart string                 `json:"period_start"`
	PeriodEnd   string                 `json:"period_end"`
	Risks       ForecastRisksJSON      `json:"risks"`
	Exhaustion  ExhaustionForecastJSON `json:"exhaustion"`
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
	trafficView, rxBytes, txBytes, totalBytes := h.currentPeriodTrafficWithBytes(r, server)
	limitView, eff, hasEff := h.limitViewWithEffective(r, page, server)
	applyQuotaTelemetry(page, &trafficView, &limitView, rxBytes, txBytes, totalBytes, eff, hasEff)
	page.AnalyticsTrafficMonth = trafficView
	page.AnalyticsLimit = limitView
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

// limitView builds the monthly traffic-limit form state for a server. On any
// read error it renders the form as "no limit" rather than failing the page —
// the limit is an optional convenience, never a hard dependency of analytics.
func (h PageHandler) limitView(r *http.Request, page view.PageData, server servers.Server) view.AnalyticsLimitView {
	v, _, _ := h.limitViewWithEffective(r, page, server)
	return v
}

func (h PageHandler) limitViewWithEffective(r *http.Request, page view.PageData, server servers.Server) (view.AnalyticsLimitView, TrafficLimit, bool) {
	v := view.AnalyticsLimitView{
		Action:        fmt.Sprintf("/servers/%d/analytics/limit", server.ID),
		UnitInput:     defaultLimitUnit,
		UnitOptions:   limitUnitOptions,
		KindInput:     LimitKindRX,
		EffectiveKind: LimitKindRX,
	}
	eff, source, ok, err := h.repo.ResolveEffectiveLimit(r.Context(), server.ID, server.Tags)
	if err != nil || !ok {
		return v, TrafficLimit{}, false
	}
	v.EffectiveKind = NormalizeLimitKind(eff.Kind)
	if source == "server" {
		v.HasLimit = true
		v.LimitHuman = humanize.Bytes(eff.Bytes)
		v.ValueInput, v.UnitInput = limitToValueUnit(eff.Bytes)
		v.KindInput = v.EffectiveKind
		v.KindLabel = limitKindLabel(page, v.EffectiveKind)
		return v, eff, true
	}
	v.InheritedHuman = humanize.Bytes(eff.Bytes)
	v.InheritedSource = limitSourceLabel(page, source)
	return v, eff, true
}

func applyQuotaTelemetry(page view.PageData, tv *view.AnalyticsTrafficSummaryView, lv *view.AnalyticsLimitView, rx, tx, total int64, eff TrafficLimit, hasEff bool) {
	if !hasEff || eff.Bytes <= 0 {
		return
	}
	kind := NormalizeLimitKind(eff.Kind)
	var used int64
	switch kind {
	case LimitKindTX:
		used = tx
	case LimitKindTotal:
		used = total
	default:
		used = rx
	}
	if used < 0 {
		used = 0
	}
	rawPct := int(math.Round(float64(used) * 100 / float64(eff.Bytes)))
	if rawPct < 0 {
		rawPct = 0
	}
	barPct := rawPct
	if barPct > 100 {
		barPct = 100
	}
	lvl := quotaLevel(rawPct)
	usedHuman := humanize.Bytes(used)
	limHuman := humanize.Bytes(eff.Bytes)
	kindLbl := limitKindLabel(page, kind)

	if tv != nil && tv.HasData {
		tv.HasQuota = true
		tv.QuotaUsedHuman = usedHuman
		tv.QuotaLimitHuman = limHuman
		tv.QuotaKindLabel = kindLbl
		tv.QuotaPct = rawPct
		tv.QuotaBarPct = barPct
		tv.QuotaLevel = lvl
	}
	if lv != nil {
		lv.HasQuota = true
		lv.QuotaUsedHuman = usedHuman
		lv.QuotaLimitHuman = limHuman
		lv.QuotaKindLabel = kindLbl
		lv.QuotaPct = rawPct
		lv.QuotaBarPct = barPct
		lv.QuotaLevel = lvl
	}
}

func quotaLevel(pct int) string {
	switch {
	case pct >= 90:
		return "crit"
	case pct >= 75:
		return "warn"
	default:
		return "ok"
	}
}

// limitKindLabel resolves the localized label for a limit kind (which traffic
// series the cap measures).
func limitKindLabel(page view.PageData, kind string) string {
	switch NormalizeLimitKind(kind) {
	case LimitKindTX:
		return page.T("analytics.limit.kind_tx")
	case LimitKindTotal:
		return page.T("analytics.limit.kind_total")
	default:
		return page.T("analytics.limit.kind_rx")
	}
}

// limitSourceLabel turns a repository source token ("global" / "tag:<name>")
// into a localized, human-readable origin for the inherited-limit hint.
func limitSourceLabel(page view.PageData, source string) string {
	if tag, ok := strings.CutPrefix(source, "tag:"); ok {
		return page.T("analytics.limits.source_tag", "tag", tag)
	}
	return page.T("analytics.limits.source_global")
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

// currentPeriodTraffic reads the server's latest vnstat snapshot and pulls out
// the current accounting period's download/upload/total.
func (h PageHandler) currentPeriodTraffic(r *http.Request, server servers.Server) view.AnalyticsTrafficSummaryView {
	summary, _, _, _ := h.currentPeriodTrafficWithBytes(r, server)
	return summary
}

func (h PageHandler) currentPeriodTrafficWithBytes(r *http.Request, server servers.Server) (view.AnalyticsTrafficSummaryView, int64, int64, int64) {
	now := time.Now().UTC()
	summary := view.AnalyticsTrafficSummaryView{MonthLabel: now.Format("2006-01")}

	days, months, err := h.repo.GetLatestTrafficForServer(r.Context(), server.ID)
	if err != nil {
		return summary, 0, 0, 0
	}

	if server.TrafficResetDay > 1 {
		summary.PeriodAnchored = true
		start := trafficPeriodStart(now, server.TrafficResetDay)
		end := start.AddDate(0, 1, 0)
		summary.MonthLabel = start.Format("2006-01-02") + " → " + end.AddDate(0, 0, -1).Format("2006-01-02")
		var rx, tx, total int64
		startLabel := start.Format("2006-01-02")
		for _, d := range days {
			if d.Label < startLabel {
				continue
			}
			rx += d.RX
			tx += d.TX
			total += seriesDayValue(d, LimitKindTotal)
		}
		if rx > 0 || tx > 0 {
			summary.HasData = true
			summary.Download = humanize.Bytes(rx)
			summary.Upload = humanize.Bytes(tx)
			summary.Total = humanize.Bytes(total)
			summary.DownloadPct, summary.UploadPct = rxTxSplit(rx, tx)
		}
		return summary, rx, tx, total
	}

	currentMonth := summary.MonthLabel
	for _, m := range months {
		if m.Label != currentMonth {
			continue
		}
		total := m.Total
		if total == 0 {
			total = m.RX + m.TX
		}
		summary.HasData = true
		summary.Download = humanize.Bytes(m.RX)
		summary.Upload = humanize.Bytes(m.TX)
		summary.Total = humanize.Bytes(total)
		summary.DownloadPct, summary.UploadPct = rxTxSplit(m.RX, m.TX)
		return summary, m.RX, m.TX, total
	}

	// Fallback to daily rows from YYYY-MM-01 on the 1st of the month when
	// vnstat's monthly rollup has not yet emitted a current-month row.
	monthStartLabel := currentMonth + "-01"
	var rx, tx, total int64
	for _, d := range days {
		if d.Label < monthStartLabel {
			continue
		}
		rx += d.RX
		tx += d.TX
		total += seriesDayValue(d, LimitKindTotal)
	}
	if rx > 0 || tx > 0 {
		summary.HasData = true
		summary.Download = humanize.Bytes(rx)
		summary.Upload = humanize.Bytes(tx)
		summary.Total = humanize.Bytes(total)
		summary.DownloadPct, summary.UploadPct = rxTxSplit(rx, tx)
	}
	return summary, rx, tx, total
}

// ── Global overview handler ───────────────────────────────────────────────────

const maxGlobalRows = 50

type GlobalHandler struct {
	deps module.Dependencies
	repo Repository
}

func NewGlobalHandler(deps module.Dependencies, repo Repository) GlobalHandler {
	return GlobalHandler{deps: deps, repo: repo}
}

func (h GlobalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	allMetrics, _ := h.repo.ListServerMetricSummaries(ctx, 0)
	allTraffic, _ := h.repo.ListServerTrafficSummaries(ctx, 0)

	var limitResolver *LimitResolver
	var tagsByServer map[int64][]string
	totalServers := 0
	if meta, ok := h.repo.(fleetMetaRepo); ok {
		if n, err := meta.CountServers(ctx); err == nil {
			totalServers = n
		}
		if lr, err := meta.LoadLimitResolver(ctx); err == nil {
			limitResolver = lr
		}
		if tm, err := meta.ListServerTagsMap(ctx); err == nil {
			tagsByServer = tm
		}
	}
	if totalServers == 0 {
		totalServers = max(len(allMetrics), len(allTraffic))
	}

	var sumCPU, sumRAM, sumDisk float64
	for _, s := range allMetrics {
		sumCPU += clampPct(s.AvgCPU)
		sumRAM += clampPct(s.AvgRAM)
		sumDisk += clampPct(s.AvgDisk)
	}

	topMetrics := allMetrics
	if len(topMetrics) > maxGlobalRows {
		topMetrics = topMetrics[:maxGlobalRows]
	}

	metricViews := make([]view.TopServerMetricView, 0, len(topMetrics))
	for i, s := range topMetrics {
		cpuPct := clampPct(s.AvgCPU)
		ramPct := clampPct(s.AvgRAM)
		diskPct := clampPct(s.AvgDisk)
		swapPct := clampPct(s.AvgSwap)
		metricViews = append(metricViews, view.TopServerMetricView{
			Rank:        i + 1,
			ServerID:    s.ServerID,
			ServerName:  s.ServerName,
			CountryCode: s.CountryCode,
			FlagEmoji:   geoip.FlagEmoji(s.CountryCode),
			CountryName: geoip.CountryName(s.CountryCode),
			CPU:         fmt.Sprintf("%.1f%%", s.AvgCPU),
			RAM:         fmt.Sprintf("%.1f%%", s.AvgRAM),
			Disk:        fmt.Sprintf("%.1f%%", s.AvgDisk),
			Swap:        fmt.Sprintf("%.1f%%", s.AvgSwap),
			CPUPct:      cpuPct,
			RAMPct:      ramPct,
			DiskPct:     diskPct,
			SwapPct:     swapPct,
			Load1:       s.LoadAvg1,
			CPULevel:    metricLevel(s.AvgCPU),
			RAMLevel:    metricLevel(s.AvgRAM),
			DiskLevel:   metricLevel(s.AvgDisk),
		})
	}

	var fleetRX, fleetTX, fleetTotal int64
	var fleetMonthLabel string
	for _, s := range allTraffic {
		fleetRX += s.MonthRX
		fleetTX += s.MonthTX
		fleetTotal += s.MonthBytes
		if fleetMonthLabel == "" && !s.PeriodAnchored && s.MonthLabel != "" {
			fleetMonthLabel = s.MonthLabel
		}
	}
	if fleetMonthLabel == "" {
		fleetMonthLabel = time.Now().UTC().Format("2006-01")
	}

	// Sort traffic descending by monthly total, then keep up to maxGlobalRows.
	sort.SliceStable(allTraffic, func(i, j int) bool {
		return allTraffic[i].MonthBytes > allTraffic[j].MonthBytes
	})
	topTraffic := allTraffic
	if len(topTraffic) > maxGlobalRows {
		topTraffic = topTraffic[:maxGlobalRows]
	}

	var maxBytes int64
	if len(topTraffic) > 0 {
		maxBytes = topTraffic[0].MonthBytes
	}

	trafficViews := make([]view.TopServerTrafficView, 0, len(topTraffic))
	for i, s := range topTraffic {
		var sharePct int
		if fleetTotal > 0 && s.MonthBytes > 0 {
			sharePct = int(math.Round(float64(s.MonthBytes) * 100 / float64(fleetTotal)))
			if sharePct > 100 {
				sharePct = 100
			}
		}
		var barPct float64
		if s.MonthBytes > 0 && maxBytes > 0 {
			barPct = math.Max(4, math.Min(100, float64(s.MonthBytes)*100/float64(maxBytes)))
		}
		rxPct, txPct := rxTxSplit(s.MonthRX, s.MonthTX)
		tv := view.TopServerTrafficView{
			Rank:        i + 1,
			ServerID:    s.ServerID,
			ServerName:  s.ServerName,
			CountryCode: s.CountryCode,
			FlagEmoji:   geoip.FlagEmoji(s.CountryCode),
			CountryName: geoip.CountryName(s.CountryCode),
			Download:    humanize.Bytes(s.MonthRX),
			Upload:      humanize.Bytes(s.MonthTX),
			MonthBytes:  humanize.Bytes(s.MonthBytes),
			RawRX:       s.MonthRX,
			RawTX:       s.MonthTX,
			RawTotal:    s.MonthBytes,
			MonthLabel:  s.MonthLabel,
			SharePct:    sharePct,
			BarPct:      barPct,
			RXBarPct:    float64(rxPct),
			TXBarPct:    float64(txPct),
		}
		if limitResolver != nil {
			if eff, _, hasEff := limitResolver.Resolve(s.ServerID, tagsByServer[s.ServerID]); hasEff && eff.Bytes > 0 {
				var used int64
				switch NormalizeLimitKind(eff.Kind) {
				case LimitKindTX:
					used = s.MonthTX
				case LimitKindTotal:
					used = s.MonthBytes
				default:
					used = s.MonthRX
				}
				qPct := int(math.Round(float64(max(int64(0), used)) * 100 / float64(eff.Bytes)))
				tv.HasQuota = true
				tv.QuotaLimitHuman = humanize.Bytes(eff.Bytes)
				tv.QuotaPct = qPct
				tv.QuotaLevel = quotaLevel(qPct)
			}
		}
		trafficViews = append(trafficViews, tv)
	}

	page := view.NewPageData(h.deps.Config, r)
	page.CSRFToken = middleware.GetCSRFToken(ctx)
	page.Title = page.T("nav.analytics")
	page.ActiveNav = "/analytics"
	page.ContentTemplate = "content-analytics-global"
	page.PageTitle = page.T("analytics.global_title")
	page.PageDescription = page.T("analytics.global_description")
	if h.deps.Database != nil {
		page.MigrationCount = h.deps.Database.MigrationCount()
	}
	globalView := view.GlobalAnalyticsView{
		TopMetrics:       metricViews,
		TopTraffic:       trafficViews,
		ServerCount:      totalServers,
		MonitoredCount:   len(allMetrics),
		ServerLimitCount: limitResolver.ServerLimitCount(),
	}
	if len(allMetrics) > 0 {
		count := float64(len(allMetrics))
		avgCPU := sumCPU / count
		avgRAM := sumRAM / count
		avgDisk := sumDisk / count
		globalView.AvgCPU = fmt.Sprintf("%.1f%%", avgCPU)
		globalView.AvgRAM = fmt.Sprintf("%.1f%%", avgRAM)
		globalView.AvgDisk = fmt.Sprintf("%.1f%%", avgDisk)
		globalView.AvgCPULevel = metricLevel(avgCPU)
		globalView.AvgRAMLevel = metricLevel(avgRAM)
		globalView.AvgDiskLevel = metricLevel(avgDisk)
	}
	if fleetTotal > 0 {
		globalView.HasFleetTraffic = true
		globalView.FleetDownloadHuman = humanize.Bytes(fleetRX)
		globalView.FleetUploadHuman = humanize.Bytes(fleetTX)
		globalView.FleetTotalHuman = humanize.Bytes(fleetTotal)
		globalView.FleetMonthLabel = fleetMonthLabel
		globalView.FleetRXPct, globalView.FleetTXPct = rxTxSplit(fleetRX, fleetTX)
	}
	if limit, ok, err := h.repo.GetScopedLimit(ctx, LimitScopeGlobal, ""); err == nil && ok {
		globalView.HasGlobalLimit = true
		globalView.GlobalLimitHuman = humanize.Bytes(limit)
	}
	if rules, err := h.repo.ListScopedLimits(ctx); err == nil {
		for _, rule := range rules {
			if rule.Scope == LimitScopeTag {
				globalView.TagLimitCount++
			}
		}
	}
	globalView.ActiveLimitRules = globalView.ServerLimitCount + globalView.TagLimitCount
	if globalView.HasGlobalLimit {
		globalView.ActiveLimitRules++
	}
	page.GlobalAnalytics = globalView
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
	metricsParam := strings.TrimSpace(r.URL.Query().Get("metrics"))
	rangeStr := strings.TrimSpace(r.URL.Query().Get("range"))

	since, rangeLabel, err := parseRange(rangeStr)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid range parameter")
		return
	}

	page := view.NewPageData(h.deps.Config, r)

	// Batched endpoint: ?metrics=cpu,ram,disk,swap,load loads the time-series
	// table ONCE and returns every requested chart in a single response.
	if metricsParam != "" {
		charts, batchErr := h.buildBatchedCharts(r, page, server.ID, strings.Split(metricsParam, ","), since, rangeLabel)
		if batchErr != nil {
			writeJSONError(w, http.StatusBadRequest, batchErr.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"range":  rangeLabel,
			"charts": charts,
		})
		return
	}

	var resp ChartDataResponse
	resp.Range = rangeLabel

	switch metric {
	case "cpu", "ram", "disk", "swap":
		resp, err = h.buildSystemChart(r, page, server.ID, metric, since, rangeLabel)
	case "load":
		resp, err = h.buildLoadChart(r, page, server.ID, since, rangeLabel)
	case "traffic":
		resp, err = h.buildTrafficChart(r, page, server.ID)
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

type systemSeriesBundle struct {
	labels   []string
	cpuVals  []float64
	ramVals  []float64
	diskVals []float64
	swapVals []float64
	load1    []float64
	load5    []float64
	load15   []float64
}

// loadSystemSeries loads raw points for short ranges (1h/6h/24h) and hourly
// rollups for 7d/30d. When a newly added server has no hourly rollups yet, it
// transparently falls back to hourly-bucketed raw snapshots so 7D/30D never
// render blank; when rollups exist, it appends the live partial current hour.
func (h DataHandler) loadSystemSeries(ctx context.Context, serverID int64, since time.Time, rangeLabel string) (systemSeriesBundle, error) {
	var b systemSeriesBundle
	if rangeLabel == "7d" || rangeLabel == "30d" {
		rollups, err := h.repo.ListHourlyRollups(ctx, serverID, since)
		if err != nil {
			return b, err
		}
		if len(rollups) == 0 {
			raw, rawErr := h.repo.ListRawSince(ctx, serverID, since)
			if rawErr != nil {
				return b, rawErr
			}
			rollups = bucketRawPointsHourly(raw)
		} else {
			tailStart := truncateToHour(rollups[len(rollups)-1].PeriodStart).Add(time.Hour)
			if tailRaw, tailErr := h.repo.ListRawSince(ctx, serverID, tailStart); tailErr == nil && len(tailRaw) > 0 {
				rollups = append(rollups, bucketRawPointsHourly(tailRaw)...)
			}
		}
		if rangeLabel == "30d" && len(rollups) > 240 {
			rollups = downsampleHourlyRollups(rollups, 3)
		}
		b.labels = make([]string, 0, len(rollups))
		for _, rp := range rollups {
			b.labels = append(b.labels, rp.PeriodStart.Format("01/02 15:04"))
			b.cpuVals = append(b.cpuVals, rp.AvgCPU)
			b.ramVals = append(b.ramVals, rp.AvgRAM)
			b.diskVals = append(b.diskVals, rp.AvgDisk)
			b.swapVals = append(b.swapVals, rp.AvgSwap)
			b.load1 = append(b.load1, rp.AvgLoad1)
			b.load5 = append(b.load5, rp.AvgLoad5)
			b.load15 = append(b.load15, rp.AvgLoad15)
		}
		return b, nil
	}

	points, err := h.repo.ListRawSince(ctx, serverID, since)
	if err != nil {
		return b, err
	}
	b.labels = make([]string, 0, len(points))
	for _, p := range points {
		b.labels = append(b.labels, formatLabel(p.RecordedAt, rangeLabel))
		b.cpuVals = append(b.cpuVals, p.CPUUsage)
		b.ramVals = append(b.ramVals, p.RAMUsage)
		b.diskVals = append(b.diskVals, p.DiskUsage)
		b.swapVals = append(b.swapVals, p.SwapUsage)
		b.load1 = append(b.load1, p.LoadAvg1)
		b.load5 = append(b.load5, p.LoadAvg5)
		b.load15 = append(b.load15, p.LoadAvg15)
	}
	return b, nil
}

func bucketRawPointsHourly(points []RawPoint) []HourlyRollup {
	if len(points) == 0 {
		return nil
	}
	var hours []time.Time
	byHour := make(map[time.Time][]RawPoint)
	for _, p := range points {
		h := truncateToHour(p.RecordedAt)
		if _, exists := byHour[h]; !exists {
			hours = append(hours, h)
		}
		byHour[h] = append(byHour[h], p)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i].Before(hours[j]) })
	out := make([]HourlyRollup, 0, len(hours))
	for _, h := range hours {
		rp := aggregateHourly(byHour[h])
		rp.PeriodStart = h
		out = append(out, rp)
	}
	return out
}

func downsampleHourlyRollups(rollups []HourlyRollup, groupSize int) []HourlyRollup {
	if groupSize <= 1 || len(rollups) <= groupSize {
		return rollups
	}
	out := make([]HourlyRollup, 0, (len(rollups)+groupSize-1)/groupSize)
	for i := 0; i < len(rollups); i += groupSize {
		end := min(i+groupSize, len(rollups))
		chunk := rollups[i:end]
		var sumCPU, sumRAM, sumDisk, sumSwap, sumL1, sumL5, sumL15 float64
		for _, rp := range chunk {
			sumCPU += rp.AvgCPU
			sumRAM += rp.AvgRAM
			sumDisk += rp.AvgDisk
			sumSwap += rp.AvgSwap
			sumL1 += rp.AvgLoad1
			sumL5 += rp.AvgLoad5
			sumL15 += rp.AvgLoad15
		}
		n := float64(len(chunk))
		out = append(out, HourlyRollup{
			PeriodStart: chunk[len(chunk)-1].PeriodStart,
			AvgCPU:      sumCPU / n,
			AvgRAM:      sumRAM / n,
			AvgDisk:     sumDisk / n,
			AvgSwap:     sumSwap / n,
			AvgLoad1:    sumL1 / n,
			AvgLoad5:    sumL5 / n,
			AvgLoad15:   sumL15 / n,
		})
	}
	return out
}

func (h DataHandler) buildBatchedCharts(r *http.Request, page view.PageData, serverID int64, rawMetrics []string, since time.Time, rangeLabel string) (map[string]ChartDataResponse, error) {
	want := make([]string, 0, len(rawMetrics))
	needSystem := false
	needTraffic := false
	seen := make(map[string]bool, len(rawMetrics))
	for _, raw := range rawMetrics {
		m := strings.TrimSpace(raw)
		if m == "" || seen[m] {
			continue
		}
		switch m {
		case "cpu", "ram", "disk", "swap", "load":
			needSystem = true
		case "traffic":
			needTraffic = true
		default:
			return nil, fmt.Errorf("unknown metric %q", m)
		}
		seen[m] = true
		want = append(want, m)
	}
	if len(want) == 0 {
		return nil, fmt.Errorf("no valid metrics requested")
	}

	var sys systemSeriesBundle
	if needSystem {
		var err error
		sys, err = h.loadSystemSeries(r.Context(), serverID, since, rangeLabel)
		if err != nil {
			return nil, err
		}
	}

	out := make(map[string]ChartDataResponse, len(want))
	for _, m := range want {
		switch m {
		case "cpu", "ram", "disk", "swap":
			color, label := localizedMetricStyle(page, m)
			var vals []float64
			switch m {
			case "cpu":
				vals = sys.cpuVals
			case "ram":
				vals = sys.ramVals
			case "disk":
				vals = sys.diskVals
			case "swap":
				vals = sys.swapVals
			}
			out[m] = ChartDataResponse{
				Metric: m,
				Range:  rangeLabel,
				Unit:   "%",
				Labels: sys.labels,
				Series: []ChartSeries{{Metric: m, Label: label, Color: color, Data: vals}},
				Min:    0,
				Max:    100,
			}
		case "load":
			out["load"] = buildLoadResponse(page, sys, rangeLabel)
		case "traffic":
			if needTraffic {
				tc, err := h.buildTrafficChart(r, page, serverID)
				if err != nil {
					return nil, err
				}
				out["traffic"] = tc
			}
		}
	}
	return out, nil
}

func (h DataHandler) buildSystemChart(r *http.Request, page view.PageData, serverID int64, metric string, since time.Time, rangeLabel string) (ChartDataResponse, error) {
	sys, err := h.loadSystemSeries(r.Context(), serverID, since, rangeLabel)
	if err != nil {
		return ChartDataResponse{}, err
	}
	var values []float64
	switch metric {
	case "cpu":
		values = sys.cpuVals
	case "ram":
		values = sys.ramVals
	case "disk":
		values = sys.diskVals
	case "swap":
		values = sys.swapVals
	}
	color, label := localizedMetricStyle(page, metric)
	return ChartDataResponse{
		Metric: metric,
		Range:  rangeLabel,
		Unit:   "%",
		Labels: sys.labels,
		Series: []ChartSeries{{Metric: metric, Label: label, Color: color, Data: values}},
		Min:    0,
		Max:    100,
	}, nil
}

func (h DataHandler) buildLoadChart(r *http.Request, page view.PageData, serverID int64, since time.Time, rangeLabel string) (ChartDataResponse, error) {
	sys, err := h.loadSystemSeries(r.Context(), serverID, since, rangeLabel)
	if err != nil {
		return ChartDataResponse{}, err
	}
	return buildLoadResponse(page, sys, rangeLabel), nil
}

func buildLoadResponse(page view.PageData, sys systemSeriesBundle, rangeLabel string) ChartDataResponse {
	return ChartDataResponse{
		Metric: "load",
		Range:  rangeLabel,
		Unit:   "",
		Labels: sys.labels,
		Series: []ChartSeries{
			{Metric: "load1", Label: page.T("analytics.series.load1"), Color: "--accent", Data: sys.load1},
			{Metric: "load5", Label: page.T("analytics.series.load5"), Color: "--ok", Data: sys.load5},
			{Metric: "load15", Label: page.T("analytics.series.load15"), Color: "--warn", Data: sys.load15},
		},
		Min: 0,
		Max: autoMax(sys.load1, sys.load5, sys.load15),
	}
}

func (h DataHandler) buildTrafficChart(r *http.Request, page view.PageData, serverID int64) (ChartDataResponse, error) {
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
		lbl := d.Label
		if len(lbl) > 5 {
			lbl = lbl[5:] // strip year → "01-15"
		}
		labels = append(labels, lbl)
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
		Unit:   "GB",
		Labels: labels,
		Series: []ChartSeries{
			{Metric: "rx", Label: page.T("analytics.download"), Color: "--accent", Data: rxData},
			{Metric: "tx", Label: page.T("analytics.upload"), Color: "--ok", Data: txData},
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
	// "no limit" so the response is identical to a server without a cap. The
	// effective limit honours the per-server > tag > global precedence so a server
	// inheriting a group/global cap forecasts against it just like an explicit one.
	limit, _, _, _ := h.repo.ResolveEffectiveLimit(r.Context(), server.ID, server.Tags)

	out := h.forecastSvc.ComputeWithConfig(days, months, ForecastConfig{Limit: limit, ResetDay: server.TrafficResetDay})
	now := time.Now().UTC()

	todayPctElapsed := int((float64(now.Hour()*60+now.Minute()) / float64(24*60)) * 100)
	weekdayOffset := int(now.Weekday())
	if weekdayOffset == 0 {
		weekdayOffset = 7
	}
	weekPctElapsed := int((float64(weekdayOffset-1)*24*60 + float64(now.Hour()*60+now.Minute())) / float64(7*24*60) * 100)
	monthPctElapsed := periodPctElapsed(now, out.PeriodStart, out.PeriodEnd)

	resp := ForecastResponseJSON{
		Today: PeriodForecastJSON{
			CurrentBytes:   out.Today.CurrentBytes,
			PredictedBytes: out.Today.PredictedBytes,
			CurrentHuman:   humanize.Bytes(out.Today.CurrentBytes),
			PredictedHuman: humanize.Bytes(out.Today.PredictedBytes),
			PctElapsed:     todayPctElapsed,
		},
		ThisWeek: PeriodForecastJSON{
			CurrentBytes:   out.ThisWeek.CurrentBytes,
			PredictedBytes: out.ThisWeek.PredictedBytes,
			CurrentHuman:   humanize.Bytes(out.ThisWeek.CurrentBytes),
			PredictedHuman: humanize.Bytes(out.ThisWeek.PredictedBytes),
			PctElapsed:     weekPctElapsed,
		},
		ThisMonth: PeriodForecastJSON{
			CurrentBytes:   out.ThisMonth.CurrentBytes,
			PredictedBytes: out.ThisMonth.PredictedBytes,
			CurrentHuman:   humanize.Bytes(out.ThisMonth.CurrentBytes),
			PredictedHuman: humanize.Bytes(out.ThisMonth.PredictedBytes),
			PctElapsed:     monthPctElapsed,
		},
		Algorithm:   out.Algorithm,
		Confidence:  string(out.Confidence),
		Trend:       string(out.Trend),
		Series:      out.Series,
		PeriodStart: out.PeriodStart,
		PeriodEnd:   out.PeriodEnd,
		Risks: ForecastRisksJSON{
			Exhaustion:    out.Risks.Exhaustion,
			TrafficSpike:  out.Risks.TrafficSpike,
			UnusualGrowth: out.Risks.UnusualGrowth,
		},
		Exhaustion: ExhaustionForecastJSON{
			HasLimit:          out.Exhaustion.HasLimit,
			LimitBytes:        out.Exhaustion.LimitBytes,
			LimitHuman:        humanize.Bytes(out.Exhaustion.LimitBytes),
			AlreadyOver:       out.Exhaustion.AlreadyOver,
			WillExhaust:       out.Exhaustion.WillExhaust,
			DaysRemaining:     out.Exhaustion.DaysRemaining,
			ExhaustionDate:    out.Exhaustion.ExhaustionDate,
			DaysUntilMonthEnd: out.Exhaustion.DaysUntilMonthEnd,
			ProjectedBytes:    out.Exhaustion.ProjectedMonth,
			ProjectedHuman:    humanize.Bytes(out.Exhaustion.ProjectedMonth),
		},
	}

	writeJSON(w, http.StatusOK, resp)
}

// ── Traffic-limit form handler ──────────────────────────────────────────────

// limitUnitOptions are the units the limit form accepts. GB and TB cover the
// realistic range of VPS monthly download caps; defaultLimitUnit pre-selects the
// most common one for a fresh form.
var limitUnitOptions = []string{"GB", "TB"}

const defaultLimitUnit = "GB"

type LimitHandler struct {
	deps       module.Dependencies
	serverRepo servers.Repository
	repo       Repository
}

func NewLimitHandler(deps module.Dependencies, serverRepo servers.Repository, repo Repository) LimitHandler {
	return LimitHandler{deps: deps, serverRepo: serverRepo, repo: repo}
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json")
}

// ServeHTTP handles POST of the monthly traffic-limit form. An empty value
// clears the limit; a positive value sets it; zero, negative, or unparseable
// input is rejected and the page is re-rendered with an inline error (or JSON
// when Accept: application/json is sent). CSRF is enforced by global middleware.
func (h LimitHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	asJSON := wantsJSON(r)
	server, ok := loadServer(w, r, h.deps, h.serverRepo)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.respondError(w, r, asJSON, server, "", defaultLimitUnit, LimitKindRX, "analytics.limit.error_invalid")
		return
	}

	rawValue := strings.TrimSpace(r.FormValue("limit_value"))
	unit := strings.TrimSpace(r.FormValue("limit_unit"))
	if !validLimitUnit(unit) {
		unit = defaultLimitUnit
	}
	kind := NormalizeLimitKind(r.FormValue("limit_kind"))

	// Empty value means "clear the limit" — a first-class action, not an error.
	if rawValue == "" {
		if err := h.repo.DeleteTrafficLimit(r.Context(), server.ID); err != nil {
			h.respondError(w, r, asJSON, server, rawValue, unit, kind, "analytics.limit.error_save")
			return
		}
		if asJSON {
			h.writeLimitJSON(w, r, server, true, "analytics.limit.flash_cleared")
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/servers/%d/analytics?flash=limit-cleared", server.ID), http.StatusSeeOther)
		return
	}

	limitBytes, ok := parseLimitBytes(rawValue, unit)
	if !ok {
		h.respondError(w, r, asJSON, server, rawValue, unit, kind, "analytics.limit.error_positive")
		return
	}

	if err := h.repo.SetTrafficLimit(r.Context(), server.ID, TrafficLimit{Bytes: limitBytes, Kind: kind}); err != nil {
		h.respondError(w, r, asJSON, server, rawValue, unit, kind, "analytics.limit.error_save")
		return
	}
	if asJSON {
		h.writeLimitJSON(w, r, server, false, "analytics.limit.flash_saved")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/servers/%d/analytics?flash=limit-saved", server.ID), http.StatusSeeOther)
}

func (h LimitHandler) respondError(w http.ResponseWriter, r *http.Request, asJSON bool, server servers.Server, value, unit, kind, errKey string) {
	if asJSON {
		page := view.NewPageData(h.deps.Config, r)
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"ok":      false,
			"message": page.T(errKey),
		})
		return
	}
	h.renderError(w, r, server, value, unit, kind, errKey)
}

func (h LimitHandler) writeLimitJSON(w http.ResponseWriter, r *http.Request, server servers.Server, cleared bool, msgKey string) {
	page := view.NewPageData(h.deps.Config, r)
	ph := PageHandler(h)
	tv, rx, tx, total := ph.currentPeriodTrafficWithBytes(r, server)
	lv, eff, hasEff := ph.limitViewWithEffective(r, page, server)
	applyQuotaTelemetry(page, &tv, &lv, rx, tx, total, eff, hasEff)

	badgeVariant := "none"
	badgeText := page.T("analytics.limit.none")
	if lv.HasLimit {
		badgeVariant = "active"
		badgeText = page.T("analytics.limit.current_kind", "value", lv.LimitHuman, "kind", lv.KindLabel)
	} else if lv.InheritedHuman != "" {
		badgeVariant = "inherited"
		badgeText = page.T("analytics.limit.inherited", "value", lv.InheritedHuman, "source", lv.InheritedSource)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"cleared":           cleared,
		"has_limit":         lv.HasLimit,
		"effective_kind":    lv.EffectiveKind,
		"badge_variant":     badgeVariant,
		"badge_text":        badgeText,
		"value_input":       lv.ValueInput,
		"unit_input":        lv.UnitInput,
		"kind_input":        lv.KindInput,
		"has_quota":         lv.HasQuota,
		"quota_used_human":  lv.QuotaUsedHuman,
		"quota_limit_human": lv.QuotaLimitHuman,
		"quota_kind_label":  lv.QuotaKindLabel,
		"quota_pct":         lv.QuotaPct,
		"quota_bar_pct":     lv.QuotaBarPct,
		"quota_level":       lv.QuotaLevel,
		"message":           page.T(msgKey),
	})
}

// renderError re-renders the analytics page with the limit form showing an
// inline validation error and the user's rejected input preserved.
func (h LimitHandler) renderError(w http.ResponseWriter, r *http.Request, server servers.Server, value, unit, kind, errKey string) {
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

	pageHandler := PageHandler(h)
	trafficView, rx, tx, total := pageHandler.currentPeriodTrafficWithBytes(r, server)
	limitView, eff, hasEff := pageHandler.limitViewWithEffective(r, page, server)
	applyQuotaTelemetry(page, &trafficView, &limitView, rx, tx, total, eff, hasEff)
	limitView.ValueInput = value
	limitView.UnitInput = unit
	limitView.KindInput = NormalizeLimitKind(kind)
	limitView.Error = page.T(errKey)
	page.AnalyticsTrafficMonth = trafficView
	page.AnalyticsLimit = limitView
	page.FlashKind = "error"
	page.FlashMessage = page.T(errKey)
	page.PageStyles = []string{"/static/analytics.css"}
	page.PageScripts = []string{"/static/analytics.js"}

	if err := h.deps.Renderer.Render(w, http.StatusUnprocessableEntity, page); err != nil {
		http.Error(w, "render analytics page", http.StatusInternalServerError)
	}
}

// periodPctElapsed returns how far (0-100) now sits inside the accounting
// period bounded by start/end ("2006-01-02"). Falls back to 0 on parse issues.
func periodPctElapsed(now time.Time, start, end string) int {
	s, err1 := time.Parse("2006-01-02", start)
	e, err2 := time.Parse("2006-01-02", end)
	if err1 != nil || err2 != nil || !e.After(s) {
		return 0
	}
	pct := int(float64(now.Sub(s)) / float64(e.Sub(s)) * 100)
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
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
// negative limits are rejected.
func parseLimitBytes(rawValue, unit string) (int64, bool) {
	value, err := strconv.ParseFloat(rawValue, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return 0, false
	}
	var mult float64
	switch unit {
	case "TB":
		mult = 1024 * 1024 * 1024 * 1024
	default: // GB
		mult = 1024 * 1024 * 1024
	}
	bytes := int64(value * mult)
	if bytes <= 0 {
		return 0, false
	}
	return bytes, true
}

// limitToValueUnit renders a stored byte count back into a form-friendly value +
// unit, preferring TB once the cap reaches 1 TB so large plans read cleanly.
func limitToValueUnit(bytes int64) (value, unit string) {
	const tib = float64(1024 * 1024 * 1024 * 1024)
	const gib = float64(1024 * 1024 * 1024)
	if float64(bytes) >= tib {
		return trimFloat(float64(bytes) / tib), "TB"
	}
	return trimFloat(float64(bytes) / gib), "GB"
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
	case "1h", "6h", "24h":
		return t.Format("15:04")
	default:
		return t.Format("01/02 15:04")
	}
}

func localizedMetricStyle(page view.PageData, metric string) (color, label string) {
	switch metric {
	case "cpu":
		return "--accent", page.T("analytics.series.cpu")
	case "ram":
		return "--ok", page.T("analytics.series.ram")
	case "disk":
		return "--warn", page.T("analytics.series.disk")
	case "swap":
		return "--an-slate-indigo", page.T("analytics.series.swap")
	default:
		return "--text-muted", metric
	}
}

func autoMax(series ...[]float64) float64 {
	var m float64
	hasVal := false
	for _, s := range series {
		for _, v := range s {
			if !hasVal || v > m {
				m = v
				hasVal = true
			}
		}
	}
	if !hasVal || m <= 0 {
		return 1
	}
	return ceilToNice(m)
}

func ceilToNice(v float64) float64 {
	if v <= 0 {
		return 1
	}
	for _, nice := range []float64{0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10, 25, 50, 100, 200, 500, 1000, 2000, 5000, 10000} {
		if v <= nice {
			return nice
		}
	}
	return v * 1.2
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func clampPct(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func metricLevel(v float64) string {
	if v > 80.0 {
		return "crit"
	}
	if v > 60.0 {
		return "warn"
	}
	return "ok"
}

func rxTxSplit(rx, tx int64) (int, int) {
	if rx <= 0 && tx <= 0 {
		return 0, 0
	}
	if rx < 0 {
		rx = 0
	}
	if tx < 0 {
		tx = 0
	}
	total := rx + tx
	if total <= 0 {
		return 0, 0
	}
	rxPct := int(math.Round(float64(rx) * 100 / float64(total)))
	if rxPct < 0 {
		rxPct = 0
	} else if rxPct > 100 {
		rxPct = 100
	}
	return rxPct, 100 - rxPct
}
