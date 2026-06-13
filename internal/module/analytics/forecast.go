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

// ForecastOutput is the complete forecast for a server.
type ForecastOutput struct {
	Today      ForecastResult
	ThisWeek   ForecastResult
	ThisMonth  ForecastResult
	Algorithm  string
	Confidence Confidence
	Trend      Trend
	Risks      ForecastRisks
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

// ForecastService selects the best algorithm for a server and computes forecasts.
type ForecastService struct {
	providers []ForecastProvider
}

func NewForecastService() *ForecastService {
	return &ForecastService{
		providers: []ForecastProvider{
			weightedMovingAverageProvider{window: 14},
			movingAverageProvider{window: 7},
			trendProvider{},
		},
	}
}

// Compute builds a full forecast from traffic history.
func (s *ForecastService) Compute(days []TrafficDay, months []TrafficMonth) ForecastOutput {
	if len(days) == 0 {
		return ForecastOutput{
			Algorithm:  "MovingAverage",
			Confidence: ConfidenceLow,
			Trend:      TrendStable,
		}
	}

	// Sort days chronologically and extract download (RX) bytes. The forecast is
	// based on DOWNLOAD traffic only — not the combined RX+TX total.
	sorted := make([]TrafficDay, len(days))
	copy(sorted, days)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Label < sorted[j].Label })

	history := make([]int64, len(sorted))
	for i, d := range sorted {
		history[i] = d.RX
	}

	// Select provider: use WeightedMA when we have ≥14 days, else SimpleMA.
	provider := s.selectProvider(history)
	dayPrediction, conf := provider.PredictDayBytes(history)

	now := time.Now().UTC()
	trend := computeTrend(history)

	// Today: sum bytes already accumulated today + predict remainder.
	todayCurrent := currentDayBytes(sorted, now)
	todayPctElapsed := float64(now.Hour()*60+now.Minute()) / float64(24*60)
	todayRemaining := float64(dayPrediction) * (1 - todayPctElapsed)
	todayPredicted := todayCurrent + int64(todayRemaining)

	// This week: sum Mon–Sun with today's partial, predict remaining days.
	weekdayOffset := int(now.Weekday())
	if weekdayOffset == 0 {
		weekdayOffset = 7
	}
	weekdayOffset-- // Monday=0
	weekStart := now.AddDate(0, 0, -weekdayOffset)
	weekCurrent := sumDaysSince(sorted, weekStart)
	remainingWeekDays := float64(7 - weekdayOffset - 1) // full days left after today
	weekPredicted := weekCurrent + int64(todayRemaining) + int64(remainingWeekDays*float64(dayPrediction))

	// This month: sum month-to-date + predict remaining days.
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthCurrent := sumDaysSince(sorted, monthStart)
	daysInMonth := float64(daysInMonth(now.Year(), now.Month()))
	dayOfMonth := float64(now.Day())
	remainingMonthDays := daysInMonth - dayOfMonth
	monthPredicted := monthCurrent + int64(todayRemaining) + int64(remainingMonthDays*float64(dayPrediction))

	risks := computeRisks(history, dayPrediction, monthCurrent, monthPredicted)

	return ForecastOutput{
		Today:     ForecastResult{CurrentBytes: todayCurrent, PredictedBytes: todayPredicted, Confidence: conf},
		ThisWeek:  ForecastResult{CurrentBytes: weekCurrent, PredictedBytes: weekPredicted, Confidence: conf},
		ThisMonth: ForecastResult{CurrentBytes: monthCurrent, PredictedBytes: monthPredicted, Confidence: conf},
		Algorithm: provider.Name(),
		Confidence: conf,
		Trend:     trend,
		Risks:     risks,
	}
}

func (s *ForecastService) selectProvider(history []int64) ForecastProvider {
	if len(history) >= 14 {
		return s.providers[0] // WeightedMovingAverage
	}
	if len(history) >= 7 {
		return s.providers[1] // MovingAverage
	}
	return s.providers[2] // LinearTrend (handles tiny datasets)
}

// currentDayBytes and sumDaysSince both sum DOWNLOAD (RX) bytes only, to match
// the download-only forecast history.
func currentDayBytes(days []TrafficDay, now time.Time) int64 {
	todayLabel := now.Format("2006-01-02")
	for _, d := range days {
		if d.Label == todayLabel {
			return d.RX
		}
	}
	return 0
}

func sumDaysSince(days []TrafficDay, since time.Time) int64 {
	sinceLabel := since.Format("2006-01-02")
	var total int64
	for _, d := range days {
		if d.Label >= sinceLabel {
			total += d.RX
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

func computeRisks(history []int64, dayPrediction, monthCurrent, monthPredicted int64) ForecastRisks {
	var avgDay int64
	if len(history) > 0 {
		var sum int64
		for _, v := range history {
			sum += v
		}
		avgDay = sum / int64(len(history))
	}

	risks := ForecastRisks{}

	// Traffic spike: today's predicted rate is 2x the historical average.
	if avgDay > 0 && dayPrediction > avgDay*2 {
		risks.TrafficSpike = true
	}

	// Unusual growth: monthly projection > 1.5x of last complete month total.
	if len(history) >= 30 {
		lastMonthTotal := sumSlice(history[len(history)-30 : len(history)-1])
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
