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

	out := svc.Compute(days, nil)
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
	cases := []struct{ year int; month time.Month; want int }{
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
