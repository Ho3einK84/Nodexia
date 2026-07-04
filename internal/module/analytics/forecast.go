package analytics

import (
	"math"
	"sort"
	"time"
)

// Confidence represents forecast reliability based on available history.
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// Trend describes the direction of traffic growth.
type Trend string

const (
	TrendIncreasing Trend = "increasing"
	TrendStable     Trend = "stable"
	TrendDecreasing Trend = "decreasing"
)

// ForecastResult is a single period prediction.
type ForecastResult struct {
	CurrentBytes   int64
	PredictedBytes int64
	Confidence     Confidence
}

// ForecastRisks flags potential issues detected from the forecast.
type ForecastRisks struct {
	Exhaustion    bool // on track to exceed any configured limit this month
	TrafficSpike  bool // current rate is 2x the historical average
	UnusualGrowth bool // 30-day trend shows >50% growth month-over-month
}

// ExhaustionForecast projects when a server with a configured monthly download
// limit will reach it. Because a monthly cap RESETS at the month boundary, the
// projection is bounded to the current month: reaching the limit is a
// within-month event, so days-to-exhaustion is never projected past month-end.
//
// States (mutually exclusive):
//   - HasLimit == false        → no limit configured; nothing to show.
//   - AlreadyOver == true      → current month RX already met/exceeded the limit.
//   - WillExhaust == true      → projected to reach the limit on ExhaustionDate,
//     DaysRemaining full days from now (0 == today).
//   - none of the above        → on track to stay under the limit all month.
type ExhaustionForecast struct {
	HasLimit          bool
	LimitBytes        int64
	AlreadyOver       bool
	WillExhaust       bool
	DaysRemaining     int    // full days from "now" to exhaustion (valid when WillExhaust)
	ExhaustionDate    string // "2006-01-02" UTC (valid when WillExhaust)
	DaysUntilMonthEnd int    // full days left in the current month, for "vs month-end" context
	ProjectedMonth    int64  // projected month-end RX (same value as ThisMonth.PredictedBytes)
}

// ForecastOutput is the complete forecast for a server. ThisMonth covers the
// current ACCOUNTING period: the calendar month by default, or the anchored
// billing period when the server has a traffic reset day configured.
type ForecastOutput struct {
	Today      ForecastResult
	ThisWeek   ForecastResult
	ThisMonth  ForecastResult
	Algorithm  string
	Confidence Confidence
	Trend      Trend
	Risks      ForecastRisks
	Exhaustion ExhaustionForecast
	// Series is the traffic series the forecast ran on: "rx" (default), "tx",
	// or "total" — it follows the configured limit kind.
	Series string
	// PeriodStart / PeriodEnd bound the current accounting period ("2006-01-02"
	// UTC, end exclusive). For a calendar month these are the first days of this
	// and next month; with a reset day they are anchored to that day.
	PeriodStart string
	PeriodEnd   string
}

// ForecastConfig carries the per-server settings that shape a forecast.
type ForecastConfig struct {
	// Limit is the effective traffic cap (Bytes <= 0 means no limit) and the
	// series it meters. History, projections, and exhaustion all follow
	// Limit.Kind so the cap is always compared against the series it measures.
	Limit TrafficLimit
	// ResetDay anchors the accounting period: <= 1 is the calendar month
	// (default), 2–28 anchors the period to that day of the month.
	ResetDay int
}

// ForecastProvider is the interface for a bandwidth forecasting algorithm.
// Future implementations (statistical, ML-based) satisfy the same interface.
type ForecastProvider interface {
	Name() string
	// PredictDayBytes returns the predicted total bytes for a full day given
	// a history of recent daily samples (oldest first).
	PredictDayBytes(history []int64) (int64, Confidence)
}

// movingAverageProvider uses a simple unweighted moving average of the last N days.
type movingAverageProvider struct{ window int }

func (p movingAverageProvider) Name() string { return "MovingAverage" }
func (p movingAverageProvider) PredictDayBytes(history []int64) (int64, Confidence) {
	if len(history) == 0 {
		return 0, ConfidenceLow
	}
	window := p.window
	if window > len(history) {
		window = len(history)
	}
	recent := history[len(history)-window:]
	var sum int64
	for _, v := range recent {
		sum += v
	}
	predicted := sum / int64(len(recent))
	conf := confidenceFromSampleCount(window)
	return predicted, conf
}

// weightedMovingAverageProvider gives exponentially higher weight to recent days.
type weightedMovingAverageProvider struct{ window int }

func (p weightedMovingAverageProvider) Name() string { return "WeightedMovingAverage" }
func (p weightedMovingAverageProvider) PredictDayBytes(history []int64) (int64, Confidence) {
	if len(history) == 0 {
		return 0, ConfidenceLow
	}
	window := p.window
	if window > len(history) {
		window = len(history)
	}
	recent := history[len(history)-window:]

	var weightedSum, totalWeight float64
	for i, v := range recent {
		weight := math.Pow(1.5, float64(i)) // exponential increase
		weightedSum += float64(v) * weight
		totalWeight += weight
	}
	if totalWeight == 0 {
		return 0, ConfidenceLow
	}
	predicted := int64(weightedSum / totalWeight)
	conf := confidenceFromSampleCount(window)
	return predicted, conf
}

// trendProvider fits a linear regression and extrapolates.
type trendProvider struct{}

func (p trendProvider) Name() string { return "LinearTrend" }
func (p trendProvider) PredictDayBytes(history []int64) (int64, Confidence) {
	n := len(history)
	if n < 3 {
		return 0, ConfidenceLow
	}
	// Simple least-squares linear regression on (x=day_index, y=bytes).
	var sumX, sumY, sumXY, sumX2 float64
	for i, v := range history {
		x := float64(i)
		y := float64(v)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}
	fn := float64(n)
	denom := fn*sumX2 - sumX*sumX
	if math.Abs(denom) < 1e-9 {
		return int64(sumY / fn), ConfidenceLow
	}
	slope := (fn*sumXY - sumX*sumY) / denom
	intercept := (sumY - slope*sumX) / fn

	nextX := float64(n)
	predicted := int64(math.Max(0, intercept+slope*nextX))
	conf := confidenceFromSampleCount(n)
	return predicted, conf
}

// DatedSample is one day of download history with its calendar date. The base
// ForecastProvider works on a bare []int64, but day-of-week seasonality needs
// the weekday of each sample, which a flat slice cannot carry.
type DatedSample struct {
	Date  time.Time
	Bytes int64
}

// weekdayForecaster is an OPTIONAL capability layered on top of ForecastProvider.
// Given dated samples it predicts the bytes for a specific future date, taking
// day-of-week seasonality into account. Providers that don't implement it fall
// back to the flat PredictDayBytes value for every future day (the existing
// behaviour), so this extends the model without breaking the base interface or
// the three non-seasonal providers.
type weekdayForecaster interface {
	// PredictForDate returns the predicted full-day bytes for date, and whether a
	// reliable weekday signal was actually used (false ⇒ caller should fall back).
	PredictForDate(samples []DatedSample, date time.Time) (int64, bool)
}

// seasonalProvider predicts each day from its day-of-week profile: the average
// of past samples that share the target weekday. PredictDayBytes still returns a
// flat trailing-window mean (the overall "level", used for spike detection and
// as a fallback); the per-weekday refinement lives in PredictForDate.
//
// Overfitting guard: a weekday is only refined when at least minPerWeekday
// samples exist for it; otherwise that day falls back to the flat level. window
// bounds how much trailing history feeds the profile.
type seasonalProvider struct {
	window        int
	minPerWeekday int
}

func (p seasonalProvider) Name() string { return "Seasonal" }

func (p seasonalProvider) PredictDayBytes(history []int64) (int64, Confidence) {
	if len(history) == 0 {
		return 0, ConfidenceLow
	}
	w := p.window
	if w > len(history) {
		w = len(history)
	}
	recent := history[len(history)-w:]
	var sum int64
	for _, v := range recent {
		sum += v
	}
	return sum / int64(len(recent)), seasonalConfidence(len(history))
}

func (p seasonalProvider) PredictForDate(samples []DatedSample, date time.Time) (int64, bool) {
	if len(samples) == 0 {
		return 0, false
	}
	if p.window > 0 && len(samples) > p.window {
		samples = samples[len(samples)-p.window:]
	}

	var total int64
	var group []int64
	for _, s := range samples {
		total += s.Bytes
		if s.Date.Weekday() == date.Weekday() {
			group = append(group, s.Bytes)
		}
	}
	// Without a positive overall level there is no meaningful signal to refine.
	if total <= 0 {
		return 0, false
	}
	// Too few samples for this weekday — don't overfit; let the caller fall back.
	if len(group) < p.minPerWeekday {
		return 0, false
	}
	var gsum int64
	for _, v := range group {
		gsum += v
	}
	return gsum / int64(len(group)), true
}

// seasonalConfidence keeps the seasonal model honest on thin data: it only
// claims High once there are four full weeks (≈4 samples per weekday); the
// 3-week activation window reports Medium.
func seasonalConfidence(n int) Confidence {
	if n >= 28 {
		return ConfidenceHigh
	}
	return ConfidenceMedium
}

// ForecastService selects the best algorithm for a server and computes forecasts.
type ForecastService struct {
	providers []ForecastProvider
	seasonal  ForecastProvider
}

// seasonalMinDays is the history length at which the day-of-week seasonal model
// activates. Three weeks gives at least three samples for every weekday, which
// is the minimum needed to estimate a weekday profile without overfitting a
// single outlier day.
const seasonalMinDays = 21

func NewForecastService() *ForecastService {
	return &ForecastService{
		providers: []ForecastProvider{
			weightedMovingAverageProvider{window: 14},
			movingAverageProvider{window: 7},
			trendProvider{},
		},
		seasonal: seasonalProvider{window: 35, minPerWeekday: 2},
	}
}

// Compute builds a full forecast from traffic history against the default
// calendar-month RX series. limitBytes is the optional monthly DOWNLOAD (RX)
// cap; pass 0 (or any non-positive value) when no limit is configured.
// Callers with a billing-cycle anchor or a non-RX limit use ComputeWithConfig.
func (s *ForecastService) Compute(days []TrafficDay, months []TrafficMonth, limitBytes int64) ForecastOutput {
	return s.ComputeWithConfig(days, months, ForecastConfig{Limit: TrafficLimit{Bytes: limitBytes, Kind: LimitKindRX}})
}

// ComputeWithConfig builds a full forecast from traffic history. The forecast
// series follows cfg.Limit.Kind (download by default) and the "this month"
// projection covers the accounting period anchored by cfg.ResetDay, so a
// provider that resets quota mid-month is projected against the right window.
func (s *ForecastService) ComputeWithConfig(days []TrafficDay, months []TrafficMonth, cfg ForecastConfig) ForecastOutput {
	kind := NormalizeLimitKind(cfg.Limit.Kind)
	if len(days) == 0 {
		return ForecastOutput{
			Algorithm:  "MovingAverage",
			Confidence: ConfidenceLow,
			Trend:      TrendStable,
			Series:     kind,
		}
	}

	// Sort days chronologically and extract the forecast series (download by
	// default; upload or the combined total when the limit meters those).
	sorted := make([]TrafficDay, len(days))
	copy(sorted, days)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Label < sorted[j].Label })

	history := make([]int64, len(sorted))
	for i, d := range sorted {
		history[i] = seriesDayValue(d, kind)
	}

	// Select provider: a day-of-week seasonal model once we have enough weeks of
	// history, otherwise the weighted/simple moving averages or linear trend.
	provider := s.selectProvider(history)
	dayPrediction, conf := provider.PredictDayBytes(history)

	// predictDay returns the predicted full-day bytes for a specific FUTURE date.
	// Seasonal providers refine it by weekday; every other provider returns the
	// flat dayPrediction for all dates, so non-seasonal behaviour is unchanged.
	predictDay := s.dayPredictor(provider, sorted, kind, dayPrediction)

	now := time.Now().UTC()
	trend := computeTrend(history)

	// Today: sum bytes already accumulated today + predict the remainder, using
	// today's own (weekday-aware) full-day estimate.
	todayCurrent := currentDayBytes(sorted, now, kind)
	todayPctElapsed := float64(now.Hour()*60+now.Minute()) / float64(24*60)
	todayRemaining := float64(predictDay(now)) * (1 - todayPctElapsed)
	todayPredicted := todayCurrent + int64(todayRemaining)

	// This week: sum Mon–Sun with today's partial, predict each remaining day
	// individually so weekday seasonality lands on the right days.
	weekdayOffset := int(now.Weekday())
	if weekdayOffset == 0 {
		weekdayOffset = 7
	}
	weekdayOffset-- // Monday=0
	weekStart := now.AddDate(0, 0, -weekdayOffset)
	weekCurrent := sumDaysSince(sorted, weekStart, kind)
	remainingWeekDays := 7 - weekdayOffset - 1 // full days left after today
	weekPredicted := weekCurrent + int64(todayRemaining) + sumFutureDays(predictDay, now, remainingWeekDays)

	// This period: the calendar month by default, or the anchored billing period
	// when a reset day is configured. For the calendar month + RX/TX/total the
	// authoritative monthly vnstat row is used — the SAME value the Analytics
	// Overview shows (the stored daily history is capped at ~5 weeks and can
	// straddle a month boundary, so summing daily rows would mis-count). Anchored
	// periods have no matching vnstat row, so they sum the daily series from the
	// period start; the 5-week daily retention covers a full 28-31 day period.
	periodStart := trafficPeriodStart(now, cfg.ResetDay)
	periodEnd := periodStart.AddDate(0, 1, 0)
	var periodCurrent int64
	if cfg.ResetDay <= 1 {
		periodCurrent = currentMonthSeries(months, now, kind)
		if periodCurrent == 0 {
			periodCurrent = sumDaysSince(sorted, periodStart, kind)
		}
	} else {
		periodCurrent = sumDaysSince(sorted, periodStart, kind)
	}
	remainingPeriodDays := fullDaysUntil(now, periodEnd)
	periodPredicted := periodCurrent + int64(todayRemaining) + sumFutureDays(predictDay, now, remainingPeriodDays)

	limitBytes := cfg.Limit.Bytes
	risks := computeRisks(history, dayPrediction, periodCurrent, periodPredicted, limitBytes)

	// Days-to-exhaustion reuses the SAME per-day predictor and today's remainder
	// that drive the period projection, so the two can never disagree on rate.
	exhaustion := computeExhaustion(now, periodCurrent, periodPredicted, limitBytes, todayRemaining, predictDay, remainingPeriodDays)

	return ForecastOutput{
		Today:       ForecastResult{CurrentBytes: todayCurrent, PredictedBytes: todayPredicted, Confidence: conf},
		ThisWeek:    ForecastResult{CurrentBytes: weekCurrent, PredictedBytes: weekPredicted, Confidence: conf},
		ThisMonth:   ForecastResult{CurrentBytes: periodCurrent, PredictedBytes: periodPredicted, Confidence: conf},
		Algorithm:   provider.Name(),
		Confidence:  conf,
		Trend:       trend,
		Risks:       risks,
		Exhaustion:  exhaustion,
		Series:      kind,
		PeriodStart: periodStart.Format("2006-01-02"),
		PeriodEnd:   periodEnd.Format("2006-01-02"),
	}
}

// TrafficPeriodStart returns the start of the accounting period containing now.
// Exported for callers (scheduler, home warnings) that need the same period
// arithmetic the forecast uses.
func TrafficPeriodStart(now time.Time, resetDay int) time.Time {
	return trafficPeriodStart(now, resetDay)
}

// trafficPeriodStart returns the start of the accounting period containing now.
// resetDay <= 1 (or out of range) means the calendar month; 2–28 anchors the
// period to that day of the month.
func trafficPeriodStart(now time.Time, resetDay int) time.Time {
	if resetDay <= 1 || resetDay > 28 {
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	anchored := time.Date(now.Year(), now.Month(), resetDay, 0, 0, 0, 0, time.UTC)
	if now.Day() >= resetDay {
		return anchored
	}
	return anchored.AddDate(0, -1, 0)
}

// fullDaysUntil returns the number of FULL days after today (UTC) before end
// (exclusive). For a calendar month it equals daysInMonth - now.Day().
func fullDaysUntil(now, end time.Time) int {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	days := int(end.Sub(today).Hours()/24) - 1
	if days < 0 {
		return 0
	}
	return days
}

// seriesDayValue / seriesMonthValue project one traffic row onto the forecast
// series. The total falls back to RX+TX when the stored total is zero, matching
// the repository's own convention.
func seriesDayValue(d TrafficDay, kind string) int64 {
	switch kind {
	case LimitKindTX:
		return d.TX
	case LimitKindTotal:
		if d.Total != 0 {
			return d.Total
		}
		return d.RX + d.TX
	default:
		return d.RX
	}
}

func seriesMonthValue(m TrafficMonth, kind string) int64 {
	switch kind {
	case LimitKindTX:
		return m.TX
	case LimitKindTotal:
		if m.Total != 0 {
			return m.Total
		}
		return m.RX + m.TX
	default:
		return m.RX
	}
}

// computeExhaustion walks the rest of the current accounting period day by day,
// accumulating the projected series until it reaches limitBytes. It returns the
// empty (HasLimit=false) value when no positive limit is set, so callers can
// omit the projection entirely for unlimited servers. The walk is bounded to
// daysUntilEnd (full days before the period resets) because the cap resets
// there — for a calendar month that is month-end.
func computeExhaustion(now time.Time, monthCurrent, monthPredicted, limitBytes int64, todayRemaining float64, predictDay func(time.Time) int64, daysUntilEnd int) ExhaustionForecast {
	if limitBytes <= 0 {
		return ExhaustionForecast{} // no limit → omit
	}

	ef := ExhaustionForecast{
		HasLimit:          true,
		LimitBytes:        limitBytes,
		DaysUntilMonthEnd: daysUntilEnd,
		ProjectedMonth:    monthPredicted,
	}

	if monthCurrent >= limitBytes {
		ef.AlreadyOver = true
		return ef
	}

	limit := float64(limitBytes)
	cum := float64(monthCurrent) + todayRemaining
	if cum >= limit {
		// Today's remaining projected usage alone crosses the limit.
		ef.WillExhaust = true
		ef.DaysRemaining = 0
		ef.ExhaustionDate = now.Format("2006-01-02")
		return ef
	}

	for i := 1; i <= daysUntilEnd; i++ {
		d := now.AddDate(0, 0, i)
		cum += float64(predictDay(d))
		if cum >= limit {
			ef.WillExhaust = true
			ef.DaysRemaining = i
			ef.ExhaustionDate = d.Format("2006-01-02")
			return ef
		}
	}

	// Not projected to reach the limit before the month resets.
	return ef
}

func (s *ForecastService) selectProvider(history []int64) ForecastProvider {
	if len(history) >= seasonalMinDays && s.seasonal != nil {
		return s.seasonal // day-of-week seasonal model
	}
	if len(history) >= 14 {
		return s.providers[0] // WeightedMovingAverage
	}
	if len(history) >= 7 {
		return s.providers[1] // MovingAverage
	}
	return s.providers[2] // LinearTrend (handles tiny datasets)
}

// dayPredictor returns a closure predicting full-day bytes for any future date.
// When the selected provider supports weekday seasonality it is consulted per
// date (falling back to flat for weekdays with too little data); otherwise every
// date maps to the flat dayPrediction — identical to the pre-seasonal behaviour.
func (s *ForecastService) dayPredictor(provider ForecastProvider, sorted []TrafficDay, kind string, flat int64) func(time.Time) int64 {
	wf, ok := provider.(weekdayForecaster)
	if !ok {
		return func(time.Time) int64 { return flat }
	}
	samples := datedSamples(sorted, kind)
	if len(samples) == 0 {
		return func(time.Time) int64 { return flat }
	}
	return func(d time.Time) int64 {
		if v, applied := wf.PredictForDate(samples, d); applied {
			return v
		}
		return flat
	}
}

// datedSamples converts sorted daily rows into dated series samples, parsing
// each "2006-01-02" label as a UTC date. Rows with an unparseable label are
// skipped rather than failing the whole forecast.
func datedSamples(sorted []TrafficDay, kind string) []DatedSample {
	out := make([]DatedSample, 0, len(sorted))
	for _, d := range sorted {
		t, err := time.Parse("2006-01-02", d.Label)
		if err != nil {
			continue
		}
		out = append(out, DatedSample{Date: t.UTC(), Bytes: seriesDayValue(d, kind)})
	}
	return out
}

// sumFutureDays sums predictDay over the n calendar days following `from`
// (from+1 … from+n). n <= 0 contributes nothing.
func sumFutureDays(predictDay func(time.Time) int64, from time.Time, n int) int64 {
	var total int64
	for i := 1; i <= n; i++ {
		total += predictDay(from.AddDate(0, 0, i))
	}
	return total
}

// currentMonthSeries returns the current month's bytes for the forecast series
// from the monthly vnstat rows — the exact metric the Analytics Overview
// displays, so the "This Month" forecast value matches the overview.
func currentMonthSeries(months []TrafficMonth, now time.Time, kind string) int64 {
	label := now.Format("2006-01")
	for _, m := range months {
		if m.Label == label {
			return seriesMonthValue(m, kind)
		}
	}
	return 0
}

// currentDayBytes and sumDaysSince both sum the forecast series, matching the
// series the history was built from.
func currentDayBytes(days []TrafficDay, now time.Time, kind string) int64 {
	todayLabel := now.Format("2006-01-02")
	for _, d := range days {
		if d.Label == todayLabel {
			return seriesDayValue(d, kind)
		}
	}
	return 0
}

func sumDaysSince(days []TrafficDay, since time.Time, kind string) int64 {
	sinceLabel := since.Format("2006-01-02")
	var total int64
	for _, d := range days {
		if d.Label >= sinceLabel {
			total += seriesDayValue(d, kind)
		}
	}
	return total
}

func computeTrend(history []int64) Trend {
	if len(history) < 7 {
		return TrendStable
	}
	// Compare last 7 days average vs previous 7 days average.
	n := len(history)
	var recentSum, prevSum int64
	recentDays := history[n-7:]
	prevDays := history[max(0, n-14) : n-7]

	for _, v := range recentDays {
		recentSum += v
	}
	if len(prevDays) == 0 {
		return TrendStable
	}
	for _, v := range prevDays {
		prevSum += v
	}

	recentAvg := float64(recentSum) / float64(len(recentDays))
	prevAvg := float64(prevSum) / float64(len(prevDays))

	if prevAvg == 0 {
		return TrendStable
	}
	ratio := recentAvg / prevAvg
	switch {
	case ratio > 1.15:
		return TrendIncreasing
	case ratio < 0.85:
		return TrendDecreasing
	default:
		return TrendStable
	}
}

func computeRisks(history []int64, dayPrediction, monthCurrent, monthPredicted, limitBytes int64) ForecastRisks {
	var avgDay int64
	if len(history) > 0 {
		var sum int64
		for _, v := range history {
			sum += v
		}
		avgDay = sum / int64(len(history))
	}

	risks := ForecastRisks{}

	// Exhaustion: a monthly download (RX) limit is configured and the projected
	// month-end RX is on track to EXCEED it. Strict greater-than means a forecast
	// that exactly meets the limit is not flagged (the cap is met, not breached).
	// monthPredicted is the same download-only projection shown in the "This
	// Month" card, so the flag is consistent with what the user sees.
	if limitBytes > 0 && monthPredicted > limitBytes {
		risks.Exhaustion = true
	}

	// Traffic spike: today's predicted rate is 2x the historical average.
	if avgDay > 0 && dayPrediction > avgDay*2 {
		risks.TrafficSpike = true
	}

	// Unusual growth: the projected month is > 1.5x the previous 30 completed
	// days. The last history element is today's still-accumulating partial day, so
	// it is excluded; the baseline is the 30 full days before it (requiring ≥ 31
	// samples). The previous slice took only 29 days and included today's partial,
	// biasing the comparison toward a false flag.
	if len(history) >= 31 {
		lastMonthTotal := sumSlice(history[len(history)-31 : len(history)-1])
		if lastMonthTotal > 0 && monthPredicted > lastMonthTotal*150/100 {
			risks.UnusualGrowth = true
		}
	}

	return risks
}

func confidenceFromSampleCount(n int) Confidence {
	switch {
	case n >= 14:
		return ConfidenceHigh
	case n >= 7:
		return ConfidenceMedium
	default:
		return ConfidenceLow
	}
}

func sumSlice(s []int64) int64 {
	var total int64
	for _, v := range s {
		total += v
	}
	return total
}

func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}
