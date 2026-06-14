package alerts_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/db"
	"github.com/Ho3einK84/Nodexia/internal/module/alerts"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
)

// evalFixture wires a temp DB, repository, a seeded server, and an enabled
// channel so the evaluator has somewhere to dispatch.
type evalFixture struct {
	ctx      context.Context
	runtime  *db.Runtime
	repo     alerts.SQLRepository
	serverID int64
	target   alerts.Target
}

func newEvalFixture(t *testing.T) evalFixture {
	t.Helper()
	runtime := testutil.OpenTestDB(t)
	ctx := context.Background()
	repo := alerts.NewSQLRepository(runtime.SQL)
	serverID := newTestServer(t, ctx, runtime, "lab-1")
	if _, err := repo.CreateChannel(ctx, alerts.Channel{
		Kind: alerts.ChannelKindTelegram, Name: "Ops", ChatID: "-100", Enabled: true,
	}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return evalFixture{ctx: ctx, runtime: runtime, repo: repo, serverID: serverID, target: alerts.Target{ID: serverID, Name: "lab-1"}}
}

func (f evalFixture) addServer(t *testing.T, name string) int64 {
	t.Helper()
	return newTestServer(t, f.ctx, f.runtime, name)
}

func (f evalFixture) mustRule(t *testing.T, rule alerts.Rule) alerts.Rule {
	t.Helper()
	created, err := f.repo.CreateRule(f.ctx, rule)
	if err != nil {
		t.Fatalf("CreateRule() error = %v", err)
	}
	return created
}

func cpuMetrics(value float64) alerts.Metrics {
	return alerts.Metrics{CPU: value}
}

func TestEvaluatorConsecutiveHitsGateFiringOnce(t *testing.T) {
	f := newEvalFixture(t)
	f.mustRule(t, alerts.Rule{
		ServerID: &f.serverID, Metric: alerts.MetricCPU, Comparator: alerts.ComparatorGTE,
		Threshold: 90, ConsecutiveHits: 3, CooldownSeconds: 900, Severity: alerts.SeverityWarning, Enabled: true,
	})

	spy := &fakeNotifier{}
	ev := alerts.NewEvaluator(f.repo, spy)

	// First two breaches stay below the consecutive-hit threshold.
	mustEvaluate(t, ev, f, cpuMetrics(95))
	mustEvaluate(t, ev, f, cpuMetrics(95))
	if spy.calls != 0 {
		t.Fatalf("fired early: calls = %d, want 0", spy.calls)
	}

	// Third consecutive breach fires exactly once.
	mustEvaluate(t, ev, f, cpuMetrics(95))
	if spy.calls != 1 {
		t.Fatalf("calls = %d, want 1 after reaching consecutive hits", spy.calls)
	}

	// A further breach within cooldown does not re-notify.
	mustEvaluate(t, ev, f, cpuMetrics(95))
	if spy.calls != 1 {
		t.Fatalf("calls = %d, want 1 (cooldown should suppress)", spy.calls)
	}
}

func TestEvaluatorCooldownThenResolve(t *testing.T) {
	f := newEvalFixture(t)
	f.mustRule(t, alerts.Rule{
		ServerID: &f.serverID, Metric: alerts.MetricCPU, Comparator: alerts.ComparatorGTE,
		Threshold: 90, ConsecutiveHits: 1, CooldownSeconds: 600, Severity: alerts.SeverityCritical, Enabled: true,
	})

	spy := &fakeNotifier{}
	current := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	ev := alerts.NewEvaluator(f.repo, spy, alerts.WithClock(func() time.Time { return current }))

	// Fires immediately (consecutive_hits = 1).
	mustEvaluate(t, ev, f, cpuMetrics(95))
	if spy.calls != 1 {
		t.Fatalf("calls = %d, want 1", spy.calls)
	}

	// Still within cooldown: no repeat.
	mustEvaluate(t, ev, f, cpuMetrics(95))
	if spy.calls != 1 {
		t.Fatalf("calls = %d, want 1 within cooldown", spy.calls)
	}

	// Past the cooldown: re-notify.
	current = current.Add(601 * time.Second)
	mustEvaluate(t, ev, f, cpuMetrics(95))
	if spy.calls != 2 {
		t.Fatalf("calls = %d, want 2 after cooldown elapsed", spy.calls)
	}

	// Recovery resolves the open event and sends a resolved message.
	current = current.Add(time.Minute)
	mustEvaluate(t, ev, f, cpuMetrics(40))
	if spy.calls != 3 {
		t.Fatalf("calls = %d, want 3 (resolved message)", spy.calls)
	}
	if !strings.Contains(spy.lastText, "RESOLVED") {
		t.Fatalf("resolved message missing RESOLVED marker:\n%s", spy.lastText)
	}
	if _, err := f.repo.GetOpenEvent(f.ctx, ruleIDFromEvents(t, f), f.serverID); !errors.Is(err, alerts.ErrNotFound) {
		t.Fatalf("expected no open event after resolve, got %v", err)
	}
}

func TestEvaluatorSilenceSuppressesFiring(t *testing.T) {
	f := newEvalFixture(t)
	f.mustRule(t, alerts.Rule{
		ServerID: &f.serverID, Metric: alerts.MetricCPU, Comparator: alerts.ComparatorGTE,
		Threshold: 90, ConsecutiveHits: 1, Severity: alerts.SeverityWarning, Enabled: true,
	})
	if _, err := f.repo.CreateSilence(f.ctx, alerts.Silence{ServerID: f.serverID, Metric: alerts.MetricCPU}); err != nil {
		t.Fatalf("CreateSilence() error = %v", err)
	}

	spy := &fakeNotifier{}
	ev := alerts.NewEvaluator(f.repo, spy)

	mustEvaluate(t, ev, f, cpuMetrics(99))
	if spy.calls != 0 {
		t.Fatalf("calls = %d, want 0 while silenced", spy.calls)
	}
	events, err := f.repo.ListRecentEvents(f.ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentEvents() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no events while silenced, got %d", len(events))
	}
}

func TestEvaluatorGlobalVsServerScopedSelection(t *testing.T) {
	f := newEvalFixture(t)
	other := f.addServer(t, "lab-2")

	// Global RAM rule applies to every server; a CPU rule scoped to a different
	// server must not fire for our target.
	f.mustRule(t, alerts.Rule{Metric: alerts.MetricRAM, Comparator: alerts.ComparatorGTE, Threshold: 80, ConsecutiveHits: 1, Severity: alerts.SeverityWarning, Enabled: true})
	f.mustRule(t, alerts.Rule{ServerID: &other, Metric: alerts.MetricCPU, Comparator: alerts.ComparatorGTE, Threshold: 90, ConsecutiveHits: 1, Severity: alerts.SeverityWarning, Enabled: true})

	spy := &fakeNotifier{}
	ev := alerts.NewEvaluator(f.repo, spy)

	mustEvaluate(t, ev, f, alerts.Metrics{RAM: 85, CPU: 99})
	if spy.calls != 1 {
		t.Fatalf("calls = %d, want 1 (only the global RAM rule applies)", spy.calls)
	}
	events, err := f.repo.ListRecentEvents(f.ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentEvents() error = %v", err)
	}
	if len(events) != 1 || events[0].Metric != alerts.MetricRAM || events[0].ServerID != f.serverID {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestEvaluatorTrafficMetrics(t *testing.T) {
	f := newEvalFixture(t)
	f.mustRule(t, alerts.Rule{ServerID: &f.serverID, Metric: alerts.MetricTrafficTotal, Comparator: alerts.ComparatorGTE, Threshold: 100, ConsecutiveHits: 1, Severity: alerts.SeverityWarning, Enabled: true})
	f.mustRule(t, alerts.Rule{ServerID: &f.serverID, Metric: alerts.MetricBandwidth, Comparator: alerts.ComparatorGTE, Threshold: 500, ConsecutiveHits: 1, Severity: alerts.SeverityWarning, Enabled: true})

	spy := &fakeNotifier{}
	ev := alerts.NewEvaluator(f.repo, spy)

	// Traffic unavailable this cycle: both traffic rules are skipped.
	mustEvaluate(t, ev, f, alerts.Metrics{TrafficAvailable: false, TrafficTotalGiB: 150, PeakMbps: 600})
	if spy.calls != 0 {
		t.Fatalf("calls = %d, want 0 when traffic is unavailable", spy.calls)
	}

	// Traffic available and breaching: both rules fire. PeakMbps is the
	// download-only (RX) peak the collector produces, so bandwidth_mbps alerts
	// on download bandwidth alone.
	mustEvaluate(t, ev, f, alerts.Metrics{TrafficAvailable: true, TrafficTotalGiB: 150, PeakMbps: 600})
	if spy.calls != 2 {
		t.Fatalf("calls = %d, want 2 (traffic_total + bandwidth_mbps)", spy.calls)
	}
}

func TestEvaluatorRecordsEventsWithoutNotifier(t *testing.T) {
	f := newEvalFixture(t)
	rule := f.mustRule(t, alerts.Rule{ServerID: &f.serverID, Metric: alerts.MetricCPU, Comparator: alerts.ComparatorGTE, Threshold: 90, ConsecutiveHits: 1, Severity: alerts.SeverityWarning, Enabled: true})

	// nil notifier: events are recorded, nothing is sent.
	ev := alerts.NewEvaluator(f.repo, nil)
	mustEvaluate(t, ev, f, cpuMetrics(95))

	if _, err := f.repo.GetOpenEvent(f.ctx, rule.ID, f.serverID); err != nil {
		t.Fatalf("expected an open event to be recorded without a notifier, got %v", err)
	}
}

func mustEvaluate(t *testing.T, ev *alerts.Evaluator, f evalFixture, metrics alerts.Metrics) {
	t.Helper()
	if err := ev.Evaluate(f.ctx, f.target, metrics); err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
}

// ruleIDFromEvents returns the rule id of the most recent event, used to look up
// the open event in assertions.
func ruleIDFromEvents(t *testing.T, f evalFixture) int64 {
	t.Helper()
	events, err := f.repo.ListRecentEvents(f.ctx, 1)
	if err != nil || len(events) == 0 || events[0].RuleID == nil {
		t.Fatalf("could not resolve rule id from events: err=%v events=%#v", err, events)
	}
	return *events[0].RuleID
}
