package analytics

import (
	"testing"
	"time"
)

func TestMovingAverageForecast(t *testing.T) {
	p := movingAverageProvider{window: 7}

	t.Run("empty history", func(t *testing.T) {
		predicted, conf := p.PredictDayBytes(nil)
		if predicted != 0 {
			t.Errorf("expected 0 for empty history, got %d", predicted)
		}
		if conf != ConfidenceLow {
			t.Errorf("expected low confidence for empty history, got %s", conf)
		}
	})

	t.Run("constant 1 GiB per day", func(t *testing.T) {
		const gib = 1024 * 1024 * 1024
		history := make([]int64, 14)
		for i := range history {
			history[i] = gib
		}
		predicted, conf := p.PredictDayBytes(history)
		if predicted != gib {
			t.Errorf("expected %d, got %d", gib, predicted)
		}
		// window=7 provider uses 7 samples → medium confidence
		if conf != ConfidenceMedium {
			t.Errorf("expected medium confidence with window=7, got %s", conf)
		}
	})

	t.Run("window capped to history length", func(t *testing.T) {
		history := []int64{100, 200, 300}
		predicted, conf := p.PredictDayBytes(history)
		if predicted != 200 {
			t.Errorf("expected avg of last 3 = 200, got %d", predicted)
		}
		if conf != ConfidenceLow {
			t.Errorf("expected low confidence with 3 samples, got %s", conf)
		}
	})
}

func TestWeightedMovingAverageForecast(t *testing.T) {
	p := weightedMovingAverageProvider{window: 7}

	t.Run("recent spike increases prediction", func(t *testing.T) {
		base := []int64{100, 100, 100, 100, 100, 100, 200}
		predicted, _ := p.PredictDayBytes(base)
		// Weighted toward the spike at the end — should be > 100
		if predicted <= 100 {
			t.Errorf("expected prediction > 100 due to recent spike, got %d", predicted)
		}
	})
}

func TestLinearTrendForecast(t *testing.T) {
	p := trendProvider{}

	t.Run("steady linear growth", func(t *testing.T) {
		// 10 data points: 100, 200, 300, ... 1000
		history := make([]int64, 10)
		for i := range history {
			history[i] = int64((i + 1) * 100)
		}
		predicted, _ := p.PredictDayBytes(history)
		// Linear regression should predict ~1100
		if predicted < 1000 || predicted > 1300 {
			t.Errorf("expected prediction ~1100 for linear growth, got %d", predicted)
		}
	})

	t.Run("too few samples returns low confidence", func(t *testing.T) {
		_, conf := p.PredictDayBytes([]int64{100, 200})
		if conf != ConfidenceLow {
			t.Errorf("expected low confidence with 2 samples, got %s", conf)
		}
	})
}

func TestComputeTrend(t *testing.T) {
	t.Run("stable traffic", func(t *testing.T) {
		history := make([]int64, 14)
		for i := range history {
			history[i] = 1000
		}
		if trend := computeTrend(history); trend != TrendStable {
			t.Errorf("expected stable trend, got %s", trend)
		}
	})

	t.Run("increasing traffic", func(t *testing.T) {
		history := make([]int64, 14)
		for i := range history {
			// Second half is 2x the first half
			if i < 7 {
				history[i] = 500
			} else {
				history[i] = 1200
			}
		}
		if trend := computeTrend(history); trend != TrendIncreasing {
			t.Errorf("expected increasing trend, got %s", trend)
		}
	})

	t.Run("decreasing traffic", func(t *testing.T) {
		history := make([]int64, 14)
		for i := range history {
			if i < 7 {
				history[i] = 1200
			} else {
				history[i] = 300
			}
		}
		if trend := computeTrend(history); trend != TrendDecreasing {
			t.Errorf("expected decreasing trend, got %s", trend)
		}
	})

	t.Run("not enough data", func(t *testing.T) {
		if trend := computeTrend([]int64{100}); trend != TrendStable {
			t.Errorf("expected stable for <7 samples, got %s", trend)
		}
	})
}

func TestForecastServiceSmokeTest(t *testing.T) {
	svc := NewForecastService()

	// Build 30 days of fake traffic data
	days := make([]TrafficDay, 30)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const dailyBytes = 5 * 1024 * 1024 * 1024 // 5 GiB/day
	for i := range days {
		days[i] = TrafficDay{
			Label: base.AddDate(0, 0, i).Format("2006-01-02"),
			RX:    dailyBytes,
			TX:    dailyBytes / 4,
			Total: dailyBytes + dailyBytes/4,
		}
	}

	out := svc.Compute(days, nil, 0)
	if out.Algorithm == "" {
		t.Error("expected non-empty algorithm name")
	}
	if out.Confidence == "" {
		t.Error("expected non-empty confidence")
	}
	if out.ThisMonth.PredictedBytes <= 0 {
		t.Errorf("expected positive monthly prediction, got %d", out.ThisMonth.PredictedBytes)
	}
}

func TestForecastUsesDownloadOnly(t *testing.T) {
	svc := NewForecastService()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const rx = 3 * 1024 * 1024 * 1024 // 3 GiB/day download

	// Same RX every day, but wildly different TX/Total between the two sets.
	low := make([]TrafficDay, 30)
	high := make([]TrafficDay, 30)
	for i := range low {
		label := base.AddDate(0, 0, i).Format("2006-01-02")
		low[i] = TrafficDay{Label: label, RX: rx, TX: 0, Total: rx}
		high[i] = TrafficDay{Label: label, RX: rx, TX: 99 * rx, Total: 100 * rx}
	}

	lo := svc.Compute(low, nil, 0)
	hi := svc.Compute(high, nil, 0)
	if lo.ThisMonth.PredictedBytes != hi.ThisMonth.PredictedBytes {
		t.Errorf("forecast must depend only on download (RX): low=%d high=%d",
			lo.ThisMonth.PredictedBytes, hi.ThisMonth.PredictedBytes)
	}
	if lo.ThisMonth.PredictedBytes <= 0 {
		t.Errorf("expected positive download forecast, got %d", lo.ThisMonth.PredictedBytes)
	}
}

func TestForecastMonthMatchesMonthlyRX(t *testing.T) {
	svc := NewForecastService()
	now := time.Now().UTC()
	monthLabel := now.Format("2006-01")

	const monthlyRX = 7 * 1024 * 1024 * 1024 * 1024 // 7 TiB authoritative month RX

	// Daily rows for the current month deliberately sum to a DIFFERENT (smaller)
	// value than the monthly row — mirroring the 7-day daily cap. The forecast's
	// "This Month" current value must follow the monthly row, not the daily sum.
	var days []TrafficDay
	for d := 1; d <= now.Day(); d++ {
		label := time.Date(now.Year(), now.Month(), d, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
		days = append(days, TrafficDay{Label: label, RX: 100 * 1024 * 1024, TX: 0, Total: 100 * 1024 * 1024})
	}
	months := []TrafficMonth{{Label: monthLabel, RX: monthlyRX, TX: 1 << 30, Total: monthlyRX + (1 << 30)}}

	out := svc.Compute(days, months, 0)
	if out.ThisMonth.CurrentBytes != monthlyRX {
		t.Errorf("ThisMonth.CurrentBytes = %d, want monthly RX %d (must match Analytics Overview)",
			out.ThisMonth.CurrentBytes, monthlyRX)
	}
	if out.ThisMonth.PredictedBytes < out.ThisMonth.CurrentBytes {
		t.Errorf("predicted month-end %d must be >= current %d",
			out.ThisMonth.PredictedBytes, out.ThisMonth.CurrentBytes)
	}
}

// weeklyPatternDays builds `weeks`*7 consecutive daily rows with a strong
// weekend/weekday split: weekends (Sat/Sun) carry `weekend` bytes, weekdays
// carry `weekday` bytes. Returned oldest-first with real calendar labels.
func weeklyPatternDays(start time.Time, weeks int, weekday, weekend int64) []TrafficDay {
	days := make([]TrafficDay, 0, weeks*7)
	for i := 0; i < weeks*7; i++ {
		d := start.AddDate(0, 0, i)
		rx := weekday
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			rx = weekend
		}
		days = append(days, TrafficDay{Label: d.Format("2006-01-02"), RX: rx, TX: 0, Total: rx})
	}
	return days
}

func TestSeasonalProviderSelected(t *testing.T) {
	svc := NewForecastService()
	// 21 days (3 weeks) is the activation threshold.
	if p := svc.selectProvider(make([]int64, 21)); p.Name() != "Seasonal" {
		t.Errorf("with 21 days, selected %q, want Seasonal", p.Name())
	}
	// 20 days must still use the pre-seasonal chain.
	if p := svc.selectProvider(make([]int64, 20)); p.Name() == "Seasonal" {
		t.Error("with 20 days, seasonal must NOT be selected")
	}
}

// TestSeasonalBeatsFlatOnWeeklyPattern is the core Phase 2 proof: on a synthetic
// weekly pattern, the seasonal per-weekday prediction is closer to the true day
// value than a flat moving average, for both a weekend and a weekday.
func TestSeasonalBeatsFlatOnWeeklyPattern(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	const weekday = 1 * gib
	const weekend = 10 * gib

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)  // Thursday
	days := weeklyPatternDays(start, 4, weekday, weekend) // 28 days
	samples := datedSamples(days)

	// Flat baseline: simple mean of the whole window (what a flat MA converges to).
	var sum int64
	for _, d := range days {
		sum += d.RX
	}
	flat := sum / int64(len(days))

	seasonal := seasonalProvider{window: 35, minPerWeekday: 2}

	// A future Saturday (weekend) and a future Tuesday (weekday).
	saturday := time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC) // Saturday
	if saturday.Weekday() != time.Saturday {
		t.Fatalf("test setup: %v is not a Saturday", saturday)
	}
	tuesday := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC) // Tuesday
	if tuesday.Weekday() != time.Tuesday {
		t.Fatalf("test setup: %v is not a Tuesday", tuesday)
	}

	satPred, ok := seasonal.PredictForDate(samples, saturday)
	if !ok {
		t.Fatal("expected seasonal prediction to apply for Saturday with 4 weeks of data")
	}
	tuePred, ok := seasonal.PredictForDate(samples, tuesday)
	if !ok {
		t.Fatal("expected seasonal prediction to apply for Tuesday with 4 weeks of data")
	}

	absDiff := func(a, b int64) int64 {
		if a > b {
			return a - b
		}
		return b - a
	}

	// Seasonal must be strictly closer to the true weekend/weekday level than flat.
	if absDiff(satPred, weekend) >= absDiff(flat, weekend) {
		t.Errorf("Saturday: seasonal err %d not better than flat err %d (pred=%d flat=%d truth=%d)",
			absDiff(satPred, weekend), absDiff(flat, weekend), satPred, flat, weekend)
	}
	if absDiff(tuePred, weekday) >= absDiff(flat, weekday) {
		t.Errorf("Tuesday: seasonal err %d not better than flat err %d (pred=%d flat=%d truth=%d)",
			absDiff(tuePred, weekday), absDiff(flat, weekday), tuePred, flat, weekday)
	}

	// Confidence honesty: 28 days → High; 21 days → Medium (not High).
	if _, c := seasonal.PredictDayBytes(make([]int64, 28)); c != ConfidenceHigh {
		t.Errorf("28-day seasonal confidence = %s, want high", c)
	}
	if _, c := seasonal.PredictDayBytes(make([]int64, 21)); c != ConfidenceMedium {
		t.Errorf("21-day seasonal confidence = %s, want medium (thin data must not claim high)", c)
	}
}

// TestSeasonalFallsBackWhenWeekdaySparse verifies the overfitting guard: a
// weekday with fewer than minPerWeekday samples is not refined.
func TestSeasonalFallsBackWhenWeekdaySparse(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Only 8 days → most weekdays appear once or twice; minPerWeekday=3 forces
	// a fallback for any weekday seen fewer than 3 times.
	days := make([]TrafficDay, 8)
	for i := range days {
		d := start.AddDate(0, 0, i)
		days[i] = TrafficDay{Label: d.Format("2006-01-02"), RX: 1000}
	}
	seasonal := seasonalProvider{window: 35, minPerWeekday: 3}
	if _, ok := seasonal.PredictForDate(datedSamples(days), start.AddDate(0, 0, 30)); ok {
		t.Error("expected seasonal fallback (ok=false) when the weekday has too few samples")
	}
}

func TestExhaustionRisk(t *testing.T) {
	const limit = 1000

	cases := []struct {
		name           string
		limit          int64
		monthPredicted int64
		want           bool
	}{
		{"limit unset never flags", 0, 999999, false},
		{"projected under limit", limit, 500, false},
		{"projected over limit", limit, 1500, true},
		{"projected exactly meets limit", limit, limit, false},
		{"negative limit treated as unset", -10, 999999, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// history/dayPrediction/monthCurrent are irrelevant to exhaustion here;
			// supply benign values so the other risk flags stay off.
			risks := computeRisks([]int64{100, 100, 100}, 100, 0, c.monthPredicted, c.limit)
			if risks.Exhaustion != c.want {
				t.Errorf("Exhaustion = %v, want %v", risks.Exhaustion, c.want)
			}
		})
	}
}

func TestExhaustionThroughCompute(t *testing.T) {
	svc := NewForecastService()
	now := time.Now().UTC()
	monthLabel := now.Format("2006-01")

	var days []TrafficDay
	for d := 1; d <= now.Day(); d++ {
		label := time.Date(now.Year(), now.Month(), d, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
		days = append(days, TrafficDay{Label: label, RX: 1 << 30, TX: 0, Total: 1 << 30})
	}
	months := []TrafficMonth{{Label: monthLabel, RX: int64(now.Day()) << 30, TX: 0, Total: int64(now.Day()) << 30}}

	tiny := svc.Compute(days, months, 1<<20) // 1 MiB — guaranteed exceeded
	if !tiny.Risks.Exhaustion {
		t.Errorf("expected exhaustion with a tiny limit, got false (predicted=%d)", tiny.ThisMonth.PredictedBytes)
	}
	huge := svc.Compute(days, months, 1<<60) // 1 EiB — never exceeded
	if huge.Risks.Exhaustion {
		t.Errorf("did not expect exhaustion with a huge limit (predicted=%d)", huge.ThisMonth.PredictedBytes)
	}
	none := svc.Compute(days, months, 0) // no limit
	if none.Risks.Exhaustion {
		t.Error("did not expect exhaustion with no limit")
	}
}

func TestComputeExhaustion(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	flat := func(time.Time) int64 { return gib } // 1 GiB/day
	// Mid-month so there is plenty of runway: Jan has 31 days.
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	const half = float64(gib) / 2 // today's remaining projected usage

	t.Run("no limit omits", func(t *testing.T) {
		ef := computeExhaustion(now, 5*gib, 9*gib, 0, half, flat)
		if ef.HasLimit {
			t.Errorf("expected HasLimit=false with no limit, got %+v", ef)
		}
	})

	t.Run("already over", func(t *testing.T) {
		ef := computeExhaustion(now, 12*gib, 30*gib, 10*gib, half, flat)
		if !ef.HasLimit || !ef.AlreadyOver || ef.WillExhaust {
			t.Errorf("expected already-over state, got %+v", ef)
		}
	})

	t.Run("exactly met counts as over", func(t *testing.T) {
		ef := computeExhaustion(now, 10*gib, 30*gib, 10*gib, half, flat)
		if !ef.AlreadyOver {
			t.Errorf("monthCurrent == limit should be already-over, got %+v", ef)
		}
	})

	t.Run("exhausts today", func(t *testing.T) {
		// 5 GiB used + 0.5 GiB remaining today crosses a 5.4 GiB limit.
		limit := 5*gib + gib/2 - 1 // < 5.5 GiB
		ef := computeExhaustion(now, 5*gib, 30*gib, limit, half, flat)
		if !ef.WillExhaust || ef.DaysRemaining != 0 {
			t.Errorf("expected exhaust today (days=0), got %+v", ef)
		}
		if ef.ExhaustionDate != "2026-01-10" {
			t.Errorf("ExhaustionDate = %q, want 2026-01-10", ef.ExhaustionDate)
		}
	})

	t.Run("exhausts in N days", func(t *testing.T) {
		// cum = 5 + 0.5 = 5.5, +1/day; crosses 10 GiB at i=5 → 2026-01-15.
		ef := computeExhaustion(now, 5*gib, 30*gib, 10*gib, half, flat)
		if !ef.WillExhaust || ef.DaysRemaining != 5 {
			t.Errorf("expected exhaust in 5 days, got %+v", ef)
		}
		if ef.ExhaustionDate != "2026-01-15" {
			t.Errorf("ExhaustionDate = %q, want 2026-01-15", ef.ExhaustionDate)
		}
		if ef.DaysUntilMonthEnd != 21 {
			t.Errorf("DaysUntilMonthEnd = %d, want 21", ef.DaysUntilMonthEnd)
		}
		if ef.ProjectedMonth != 30*gib {
			t.Errorf("ProjectedMonth = %d, want %d", ef.ProjectedMonth, 30*gib)
		}
	})

	t.Run("comfortably under all month", func(t *testing.T) {
		ef := computeExhaustion(now, 5*gib, 30*gib, 1000*gib, half, flat)
		if !ef.HasLimit || ef.AlreadyOver || ef.WillExhaust {
			t.Errorf("expected under-limit state, got %+v", ef)
		}
	})

	t.Run("month boundary: last day, no full days left", func(t *testing.T) {
		eom := time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC)
		// Not crossed by today's remainder, and there are no further days to walk.
		ef := computeExhaustion(eom, 5*gib, 6*gib, 10*gib, half, flat)
		if ef.WillExhaust {
			t.Errorf("expected no exhaustion on last day when under, got %+v", ef)
		}
		if ef.DaysUntilMonthEnd != 0 {
			t.Errorf("DaysUntilMonthEnd = %d, want 0 on the last day", ef.DaysUntilMonthEnd)
		}
		// But today's remainder alone can still cross on the last day.
		ef2 := computeExhaustion(eom, 5*gib, 6*gib, 5*gib+gib/2-1, half, flat)
		if !ef2.WillExhaust || ef2.DaysRemaining != 0 || ef2.ExhaustionDate != "2026-01-31" {
			t.Errorf("expected exhaust-today on last day, got %+v", ef2)
		}
	})
}

func TestExhaustionPlumbedThroughCompute(t *testing.T) {
	svc := NewForecastService()
	now := time.Now().UTC()
	var days []TrafficDay
	for d := 1; d <= now.Day(); d++ {
		label := time.Date(now.Year(), now.Month(), d, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
		days = append(days, TrafficDay{Label: label, RX: 1 << 30})
	}
	out := svc.Compute(days, nil, 1<<40) // 1 TiB limit
	if !out.Exhaustion.HasLimit {
		t.Error("expected Exhaustion.HasLimit when a limit is configured")
	}
	if out.Exhaustion.ProjectedMonth != out.ThisMonth.PredictedBytes {
		t.Errorf("ProjectedMonth %d must equal ThisMonth.PredictedBytes %d (single rate)",
			out.Exhaustion.ProjectedMonth, out.ThisMonth.PredictedBytes)
	}
	// No limit → omitted.
	none := svc.Compute(days, nil, 0)
	if none.Exhaustion.HasLimit {
		t.Error("expected Exhaustion omitted when no limit is configured")
	}
}

func TestParseLimitBytes(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	const tib = gib * 1024

	cases := []struct {
		value, unit string
		wantBytes   int64
		wantOK      bool
	}{
		{"500", "GiB", 500 * gib, true},
		{"1", "TiB", tib, true},
		{"0.5", "TiB", tib / 2, true},
		{"0", "GiB", 0, false},
		{"-5", "GiB", 0, false},
		{"abc", "GiB", 0, false},
		{"", "GiB", 0, false},
		{"100", "", 100 * gib, true}, // unknown unit falls back to GiB
	}
	for _, c := range cases {
		gotBytes, gotOK := parseLimitBytes(c.value, c.unit)
		if gotOK != c.wantOK {
			t.Errorf("parseLimitBytes(%q,%q) ok = %v, want %v", c.value, c.unit, gotOK, c.wantOK)
			continue
		}
		if gotOK && gotBytes != c.wantBytes {
			t.Errorf("parseLimitBytes(%q,%q) = %d, want %d", c.value, c.unit, gotBytes, c.wantBytes)
		}
	}
}

func TestLimitToValueUnit(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	const tib = gib * 1024
	cases := []struct {
		bytes     int64
		wantValue string
		wantUnit  string
	}{
		{500 * gib, "500", "GiB"},
		{tib, "1", "TiB"},
		{tib + tib/2, "1.5", "TiB"},
	}
	for _, c := range cases {
		v, u := limitToValueUnit(c.bytes)
		if v != c.wantValue || u != c.wantUnit {
			t.Errorf("limitToValueUnit(%d) = (%q,%q), want (%q,%q)", c.bytes, v, u, c.wantValue, c.wantUnit)
		}
	}
}

func TestRollupHelpers(t *testing.T) {
	t.Run("truncateToHour", func(t *testing.T) {
		ts := time.Date(2026, 3, 15, 14, 35, 22, 0, time.UTC)
		got := truncateToHour(ts)
		want := time.Date(2026, 3, 15, 14, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("expected %v, got %v", want, got)
		}
	})

	t.Run("truncateToDay", func(t *testing.T) {
		ts := time.Date(2026, 3, 15, 14, 35, 22, 0, time.UTC)
		got := truncateToDay(ts)
		want := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("expected %v, got %v", want, got)
		}
	})

	t.Run("aggregateHourly empty", func(t *testing.T) {
		r := aggregateHourly(nil)
		if r.SampleCount != 0 {
			t.Errorf("expected 0 sample count for empty input, got %d", r.SampleCount)
		}
	})

	t.Run("aggregateHourly averages correctly", func(t *testing.T) {
		pts := []RawPoint{
			{CPUUsage: 10, RAMUsage: 20, DiskUsage: 30, SwapUsage: 5, LoadAvg1: 1, LoadAvg5: 2, LoadAvg15: 3},
			{CPUUsage: 30, RAMUsage: 40, DiskUsage: 50, SwapUsage: 15, LoadAvg1: 3, LoadAvg5: 4, LoadAvg15: 5},
		}
		r := aggregateHourly(pts)
		if r.AvgCPU != 20 {
			t.Errorf("expected AvgCPU=20, got %f", r.AvgCPU)
		}
		if r.AvgRAM != 30 {
			t.Errorf("expected AvgRAM=30, got %f", r.AvgRAM)
		}
		if r.SampleCount != 2 {
			t.Errorf("expected SampleCount=2, got %d", r.SampleCount)
		}
	})
}

func TestDaysInMonth(t *testing.T) {
	cases := []struct {
		year  int
		month time.Month
		want  int
	}{
		{2024, time.February, 29}, // leap year
		{2023, time.February, 28},
		{2026, time.January, 31},
		{2026, time.April, 30},
	}
	for _, c := range cases {
		got := daysInMonth(c.year, c.month)
		if got != c.want {
			t.Errorf("daysInMonth(%d, %s) = %d, want %d", c.year, c.month, got, c.want)
		}
	}
}
