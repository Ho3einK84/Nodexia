package alerts_test

import (
	"strings"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/module/alerts"
)

// TestEvaluatorPredictiveUnavailableNoFire verifies the forecast-derived metrics
// are skipped (never fired, never recorded) when the forecast is unavailable —
// the case for a server without a monthly limit. ProjectedExceedsLimit is set to
// true here on purpose to prove the availability gate, not the value, decides.
func TestEvaluatorPredictiveUnavailableNoFire(t *testing.T) {
	f := newEvalFixture(t)
	f.mustRule(t, alerts.Rule{
		ServerID: &f.serverID, Metric: alerts.MetricProjectedExceedLimit,
		Comparator: alerts.ComparatorGTE, Threshold: 1, ConsecutiveHits: 1,
		Severity: alerts.SeverityWarning, Enabled: true,
	})
	f.mustRule(t, alerts.Rule{
		ServerID: &f.serverID, Metric: alerts.MetricDaysToExhaustion,
		Comparator: alerts.ComparatorLTE, Threshold: 3, ConsecutiveHits: 1,
		Severity: alerts.SeverityWarning, Enabled: true,
	})

	spy := &fakeNotifier{}
	ev := alerts.NewEvaluator(f.repo, spy)

	mustEvaluate(t, ev, f, alerts.Metrics{
		ForecastAvailable:     false,
		ProjectedExceedsLimit: true,
		DaysToExhaustion:      0, // would breach ≤ 3, but the metric is unavailable
	})
	if spy.calls != 0 {
		t.Fatalf("calls = %d, want 0 when the forecast is unavailable", spy.calls)
	}
	events, err := f.repo.ListRecentEvents(f.ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentEvents() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no events while the forecast is unavailable, got %d", len(events))
	}
}

// TestEvaluatorProjectedExceedLimitFiresAndResolves covers the boolean projection
// modelled as a 0/1 value: it fires once the month is projected over the limit
// and resolves when the projection falls back under it.
func TestEvaluatorProjectedExceedLimitFiresAndResolves(t *testing.T) {
	f := newEvalFixture(t)
	f.mustRule(t, alerts.Rule{
		ServerID: &f.serverID, Metric: alerts.MetricProjectedExceedLimit,
		Comparator: alerts.ComparatorGTE, Threshold: 1, ConsecutiveHits: 1,
		Severity: alerts.SeverityCritical, Enabled: true,
	})

	spy := &fakeNotifier{}
	ev := alerts.NewEvaluator(f.repo, spy)

	// Projected over → value 1 ≥ 1 → fires once.
	mustEvaluate(t, ev, f, alerts.Metrics{ForecastAvailable: true, ProjectedExceedsLimit: true})
	if spy.calls != 1 {
		t.Fatalf("calls = %d, want 1 when projected over the limit", spy.calls)
	}

	// Projection improves → value 0, not ≥ 1 → resolves.
	mustEvaluate(t, ev, f, alerts.Metrics{ForecastAvailable: true, ProjectedExceedsLimit: false})
	if spy.calls != 2 {
		t.Fatalf("calls = %d, want 2 (resolved message)", spy.calls)
	}
	if !strings.Contains(spy.lastText, "RESOLVED") {
		t.Fatalf("resolved message missing RESOLVED marker:\n%s", spy.lastText)
	}
}

// TestEvaluatorDaysToExhaustionFiresAndResolves covers the "lower is worse" days
// metric: it fires when the days remaining drop to/below the threshold and
// resolves once the projection returns to the safe sentinel (no longer projected
// to exhaust this month).
func TestEvaluatorDaysToExhaustionFiresAndResolves(t *testing.T) {
	f := newEvalFixture(t)
	f.mustRule(t, alerts.Rule{
		ServerID: &f.serverID, Metric: alerts.MetricDaysToExhaustion,
		Comparator: alerts.ComparatorLTE, Threshold: 3, ConsecutiveHits: 1,
		Severity: alerts.SeverityWarning, Enabled: true,
	})

	spy := &fakeNotifier{}
	ev := alerts.NewEvaluator(f.repo, spy)

	// 5 days left → above the ≤ 3 threshold → no fire.
	mustEvaluate(t, ev, f, alerts.Metrics{ForecastAvailable: true, DaysToExhaustion: 5})
	if spy.calls != 0 {
		t.Fatalf("calls = %d, want 0 at 5 days remaining", spy.calls)
	}

	// 2 days left → at/below the threshold → fires once.
	mustEvaluate(t, ev, f, alerts.Metrics{ForecastAvailable: true, DaysToExhaustion: 2})
	if spy.calls != 1 {
		t.Fatalf("calls = %d, want 1 at 2 days remaining", spy.calls)
	}

	// No longer projected to exhaust → safe sentinel → resolves.
	mustEvaluate(t, ev, f, alerts.Metrics{ForecastAvailable: true, DaysToExhaustion: alerts.DaysToExhaustionSafe})
	if spy.calls != 2 {
		t.Fatalf("calls = %d, want 2 (resolved message)", spy.calls)
	}
	if !strings.Contains(spy.lastText, "RESOLVED") {
		t.Fatalf("resolved message missing RESOLVED marker:\n%s", spy.lastText)
	}
}

// TestEvaluatorDaysToExhaustionAvailabilityGapPreservesStreak verifies an
// unavailable cycle (the server temporarily has no forecast) neither fires,
// resolves, nor resets an in-progress consecutive-breach streak.
func TestEvaluatorDaysToExhaustionAvailabilityGapPreservesStreak(t *testing.T) {
	f := newEvalFixture(t)
	f.mustRule(t, alerts.Rule{
		ServerID: &f.serverID, Metric: alerts.MetricDaysToExhaustion,
		Comparator: alerts.ComparatorLTE, Threshold: 3, ConsecutiveHits: 2,
		Severity: alerts.SeverityWarning, Enabled: true,
	})

	spy := &fakeNotifier{}
	ev := alerts.NewEvaluator(f.repo, spy)

	// Breach #1 (streak 1/2): no fire yet.
	mustEvaluate(t, ev, f, alerts.Metrics{ForecastAvailable: true, DaysToExhaustion: 1})
	if spy.calls != 0 {
		t.Fatalf("calls = %d, want 0 after first breach", spy.calls)
	}

	// Availability gap: forecast unavailable. Must not reset the streak.
	mustEvaluate(t, ev, f, alerts.Metrics{ForecastAvailable: false, DaysToExhaustion: 1})
	if spy.calls != 0 {
		t.Fatalf("calls = %d, want 0 during availability gap", spy.calls)
	}

	// Breach #2 (streak reaches 2/2): fires. If the gap had reset the streak, this
	// would only be the first breach again and would not fire.
	mustEvaluate(t, ev, f, alerts.Metrics{ForecastAvailable: true, DaysToExhaustion: 1})
	if spy.calls != 1 {
		t.Fatalf("calls = %d, want 1 once the streak survives the gap and reaches 2", spy.calls)
	}
}
