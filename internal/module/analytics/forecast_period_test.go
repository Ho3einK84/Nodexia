package analytics

import (
	"testing"
	"time"
)

func TestTrafficPeriodStart(t *testing.T) {
	cases := []struct {
		name     string
		now      time.Time
		resetDay int
		want     string
	}{
		{"calendar default", time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC), 1, "2026-06-01"},
		{"zero falls back to calendar", time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC), 0, "2026-06-01"},
		{"out of range falls back to calendar", time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC), 31, "2026-06-01"},
		{"after anchor day", time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC), 15, "2026-06-15"},
		{"on anchor day", time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC), 15, "2026-06-15"},
		{"before anchor day wraps to previous month", time.Date(2026, 6, 10, 10, 0, 0, 0, time.UTC), 15, "2026-05-15"},
		{"january wraps to december", time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), 20, "2025-12-20"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TrafficPeriodStart(tc.now, tc.resetDay).Format("2006-01-02")
			if got != tc.want {
				t.Fatalf("TrafficPeriodStart(%s, %d) = %s, want %s", tc.now.Format("2006-01-02"), tc.resetDay, got, tc.want)
			}
		})
	}
}

func TestSeriesValues(t *testing.T) {
	day := TrafficDay{RX: 10, TX: 4, Total: 0}
	if v := seriesDayValue(day, LimitKindRX); v != 10 {
		t.Fatalf("rx = %d, want 10", v)
	}
	if v := seriesDayValue(day, LimitKindTX); v != 4 {
		t.Fatalf("tx = %d, want 4", v)
	}
	// Total falls back to RX+TX when the stored total is zero.
	if v := seriesDayValue(day, LimitKindTotal); v != 14 {
		t.Fatalf("total fallback = %d, want 14", v)
	}
	if v := seriesDayValue(TrafficDay{RX: 10, TX: 4, Total: 15}, LimitKindTotal); v != 15 {
		t.Fatalf("total stored = %d, want 15", v)
	}
	month := TrafficMonth{RX: 100, TX: 40}
	if v := seriesMonthValue(month, LimitKindTotal); v != 140 {
		t.Fatalf("month total fallback = %d, want 140", v)
	}
}

// TestComputeWithConfigTXSeries proves the forecast pivots to the limited
// series: identical history with heavy TX and light RX must exhaust a TX limit
// but not the same-sized RX limit.
func TestComputeWithConfigTXSeries(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	now := time.Now().UTC()
	days := make([]TrafficDay, 14)
	for i := range days {
		d := now.AddDate(0, 0, -(len(days) - 1 - i))
		days[i] = TrafficDay{Label: d.Format("2006-01-02"), RX: 1 * gib / 100, TX: 10 * gib}
	}
	svc := NewForecastService()
	limit := TrafficLimit{Bytes: 20 * gib}

	rxOut := svc.ComputeWithConfig(days, nil, ForecastConfig{Limit: TrafficLimit{Bytes: limit.Bytes, Kind: LimitKindRX}})
	txOut := svc.ComputeWithConfig(days, nil, ForecastConfig{Limit: TrafficLimit{Bytes: limit.Bytes, Kind: LimitKindTX}})

	if rxOut.Series != LimitKindRX || txOut.Series != LimitKindTX {
		t.Fatalf("series = %q/%q, want rx/tx", rxOut.Series, txOut.Series)
	}
	if rxOut.Exhaustion.AlreadyOver || rxOut.Exhaustion.WillExhaust {
		t.Fatalf("tiny RX must not exhaust an RX limit: %+v", rxOut.Exhaustion)
	}
	if !txOut.Exhaustion.AlreadyOver && !txOut.Exhaustion.WillExhaust {
		t.Fatalf("heavy TX must exhaust a TX limit: %+v", txOut.Exhaustion)
	}
}

// TestComputeWithConfigAnchoredPeriod proves the accounting period follows the
// reset day: the period bounds land on the anchor and the projection never
// walks past the period end.
func TestComputeWithConfigAnchoredPeriod(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	now := time.Now().UTC()

	// Pick a reset day guaranteed to differ from today's day-of-month semantics:
	// anchor on yesterday's day (clamped to 2..28) so the period started recently.
	anchor := now.AddDate(0, 0, -1).Day()
	if anchor < 2 {
		anchor = 2
	}
	if anchor > 28 {
		anchor = 28
	}

	days := make([]TrafficDay, 14)
	for i := range days {
		d := now.AddDate(0, 0, -(len(days) - 1 - i))
		days[i] = TrafficDay{Label: d.Format("2006-01-02"), RX: 1 * gib}
	}
	svc := NewForecastService()
	out := svc.ComputeWithConfig(days, nil, ForecastConfig{
		Limit:    TrafficLimit{Bytes: 1000 * gib, Kind: LimitKindRX},
		ResetDay: anchor,
	})

	wantStart := TrafficPeriodStart(now, anchor)
	if out.PeriodStart != wantStart.Format("2006-01-02") {
		t.Fatalf("PeriodStart = %s, want %s", out.PeriodStart, wantStart.Format("2006-01-02"))
	}
	wantEnd := wantStart.AddDate(0, 1, 0)
	if out.PeriodEnd != wantEnd.Format("2006-01-02") {
		t.Fatalf("PeriodEnd = %s, want %s", out.PeriodEnd, wantEnd.Format("2006-01-02"))
	}
	if got, want := out.Exhaustion.DaysUntilMonthEnd, fullDaysUntil(now, wantEnd); got != want {
		t.Fatalf("DaysUntilMonthEnd = %d, want %d (full days before the anchored reset)", got, want)
	}

	// The anchored period total must be the sum of daily rows inside the period,
	// NOT a calendar-month value.
	startLabel := wantStart.Format("2006-01-02")
	var want int64
	for _, d := range days {
		if d.Label >= startLabel {
			want += d.RX
		}
	}
	if out.ThisMonth.CurrentBytes != want {
		t.Fatalf("ThisMonth.CurrentBytes = %d, want %d (daily sum since %s)", out.ThisMonth.CurrentBytes, want, startLabel)
	}
}

func TestFullDaysUntil(t *testing.T) {
	now := time.Date(2026, 1, 30, 13, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	// Jan 31 is the only full day left before Feb 1.
	if got := fullDaysUntil(now, end); got != 1 {
		t.Fatalf("fullDaysUntil = %d, want 1", got)
	}
	// Period ending tomorrow → no full days left.
	if got := fullDaysUntil(now, time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)); got != 0 {
		t.Fatalf("fullDaysUntil(tomorrow) = %d, want 0", got)
	}
}
