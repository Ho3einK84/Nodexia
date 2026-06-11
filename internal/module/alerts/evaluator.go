package alerts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/notify"
)

// Target identifies the server an evaluation runs against. Only the id and name
// are needed, keeping the evaluator decoupled from the full server record.
type Target struct {
	ID   int64
	Name string
}

// Metrics carries the freshly collected values for one monitoring cycle. The
// scheduler builds it from the snapshot it already stored, so evaluation never
// re-queries SSH. TrafficAvailable gates the traffic-derived metrics: when the
// vnStat snapshot was not collected, traffic_total and bandwidth_mbps rules are
// skipped for this cycle.
type Metrics struct {
	CPU    float64
	RAM    float64
	Disk   float64
	Load1  float64
	Load5  float64
	Load15 float64

	TrafficAvailable bool
	TrafficTotalGiB  float64
	PeakMbps         float64
	AvgMbps          float64
}

// valueFor returns the observed value for a metric and whether it is available
// this cycle.
func (m Metrics) valueFor(metric string) (float64, bool) {
	switch metric {
	case MetricCPU:
		return m.CPU, true
	case MetricRAM:
		return m.RAM, true
	case MetricDisk:
		return m.Disk, true
	case MetricLoad1:
		return m.Load1, true
	case MetricLoad5:
		return m.Load5, true
	case MetricLoad15:
		return m.Load15, true
	case MetricTrafficTotal:
		return m.TrafficTotalGiB, m.TrafficAvailable
	case MetricBandwidth:
		// bandwidth_mbps alerts on the peak sample for the period.
		return m.PeakMbps, m.TrafficAvailable
	default:
		return 0, false
	}
}

// Evaluator turns collected metrics into firing/resolved alert events and
// dispatches notifications. Persisted alert_events are the source of truth for
// open alerts and notification timing, so firing/resolved state and cooldown
// survive restarts; only the consecutive-breach streak is kept in memory and is
// allowed to reset on restart (an already-open event keeps re-notifying via the
// persisted notified_at).
type Evaluator struct {
	repo     Repository
	notifier notify.Notifier // nil when no Telegram token is configured
	clock    func() time.Time

	mu      sync.Mutex
	streaks map[streakKey]int
}

type streakKey struct {
	ruleID   int64
	serverID int64
}

// EvaluatorOption customizes an Evaluator.
type EvaluatorOption func(*Evaluator)

// WithClock overrides the time source (used by tests to drive cooldowns).
func WithClock(clock func() time.Time) EvaluatorOption {
	return func(e *Evaluator) {
		if clock != nil {
			e.clock = clock
		}
	}
}

// NewEvaluator builds an Evaluator. A nil notifier means sending is disabled:
// events are still recorded, but no messages are dispatched.
func NewEvaluator(repo Repository, notifier notify.Notifier, opts ...EvaluatorOption) *Evaluator {
	e := &Evaluator{
		repo:     repo,
		notifier: notifier,
		clock:    func() time.Time { return time.Now().UTC() },
		streaks:  map[streakKey]int{},
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Evaluate runs every enabled rule (global + server-specific) against the
// metrics for one server. It returns the first error encountered but always
// attempts every rule; callers should log the error without failing the
// monitoring job.
func (e *Evaluator) Evaluate(ctx context.Context, target Target, metrics Metrics) error {
	rules, err := e.repo.ListEnabledRulesForServer(ctx, target.ID)
	if err != nil {
		return fmt.Errorf("alerts: list rules for server %d: %w", target.ID, err)
	}

	var firstErr error
	for _, rule := range rules {
		if err := e.evaluateRule(ctx, target, rule, metrics); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (e *Evaluator) evaluateRule(ctx context.Context, target Target, rule Rule, metrics Metrics) error {
	value, ok := metrics.valueFor(rule.Metric)
	if !ok {
		// Metric not collected this cycle (e.g. traffic snapshot unavailable).
		return nil
	}

	now := e.clock()
	breach := Breaches(rule.Comparator, value, rule.Threshold)

	open, openErr := e.repo.GetOpenEvent(ctx, rule.ID, target.ID)
	hasOpen := openErr == nil
	if openErr != nil && !errors.Is(openErr, ErrNotFound) {
		return openErr
	}

	// Recovery: value is back below the threshold. Resolve any open event,
	// regardless of silence (a silence suppresses firing, not resolution).
	if !breach {
		e.resetStreak(rule, target)
		if hasOpen {
			return e.resolve(ctx, target, rule, open, value, now)
		}
		return nil
	}

	silenced, err := e.repo.IsSilenced(ctx, target.ID, rule.Metric)
	if err != nil {
		return err
	}
	if silenced {
		// Do not fire or re-notify while muted; a silenced breach does not count
		// toward the consecutive-hit streak.
		e.resetStreak(rule, target)
		return nil
	}

	if hasOpen {
		// Already firing: re-notify only once the cooldown has elapsed.
		if e.cooldownElapsed(rule, open, now) {
			e.dispatch(ctx, rule, target, e.message(rule, target, value, notify.StateFiring, now))
			if err := e.repo.MarkEventNotified(ctx, open.ID, now); err != nil {
				return err
			}
		}
		return nil
	}

	// Not yet firing: accumulate consecutive breaches before transitioning.
	if e.incStreak(rule, target) < rule.ConsecutiveHits {
		return nil
	}
	return e.fire(ctx, target, rule, value, now)
}

func (e *Evaluator) fire(ctx context.Context, target Target, rule Rule, value float64, now time.Time) error {
	notifiedAt := now
	_, err := e.repo.CreateEvent(ctx, Event{
		RuleID:        ruleEventID(rule),
		ServerID:      target.ID,
		Metric:        rule.Metric,
		ObservedValue: value,
		Threshold:     rule.Threshold,
		Severity:      rule.Severity,
		State:         EventStateFiring,
		FiredAt:       now,
		NotifiedAt:    &notifiedAt,
	})
	if err != nil {
		return err
	}

	e.resetStreak(rule, target)
	e.dispatch(ctx, rule, target, e.message(rule, target, value, notify.StateFiring, now))
	return nil
}

func (e *Evaluator) resolve(ctx context.Context, target Target, rule Rule, open Event, value float64, now time.Time) error {
	if err := e.repo.ResolveEvent(ctx, open.ID, now); err != nil {
		return err
	}
	e.dispatch(ctx, rule, target, e.message(rule, target, value, notify.StateResolved, now))
	return nil
}

// cooldownElapsed reports whether enough time has passed since the open event
// was last notified to re-notify. A zero/absent notified_at always re-notifies.
func (e *Evaluator) cooldownElapsed(rule Rule, open Event, now time.Time) bool {
	if open.NotifiedAt == nil {
		return true
	}
	return !now.Before(open.NotifiedAt.Add(time.Duration(rule.CooldownSeconds) * time.Second))
}

// dispatch renders and sends a message to the rule's target channels. Sending
// is best-effort: a nil notifier (no token) or a per-channel failure is logged
// and never aborts evaluation.
func (e *Evaluator) dispatch(ctx context.Context, rule Rule, target Target, msg notify.AlertMessage) {
	if e.notifier == nil {
		return
	}

	channels, err := e.targetChannels(ctx, rule)
	if err != nil {
		slog.Warn("alert dispatch: load channels failed",
			slog.Int64("rule_id", rule.ID),
			slog.String("error", err.Error()),
		)
		return
	}

	for _, channel := range channels {
		text, err := notify.RenderMessage(channel.MessageTemplate, msg)
		if err != nil {
			slog.Warn("alert dispatch: render message failed",
				slog.Int64("channel_id", channel.ID),
				slog.String("error", err.Error()),
			)
			continue
		}
		if err := e.notifier.Send(ctx, channel.ChatID, text); err != nil {
			slog.Warn("alert dispatch: send failed",
				slog.Int64("channel_id", channel.ID),
				slog.Int64("server_id", target.ID),
				slog.String("error", err.Error()),
			)
		}
	}
}

// targetChannels returns the channels a rule notifies: its specific channel when
// set and enabled, otherwise every enabled channel.
func (e *Evaluator) targetChannels(ctx context.Context, rule Rule) ([]Channel, error) {
	if rule.ChannelID == nil {
		return e.repo.ListEnabledChannels(ctx)
	}

	channel, err := e.repo.GetChannel(ctx, *rule.ChannelID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if !channel.Enabled {
		return nil, nil
	}
	return []Channel{channel}, nil
}

func (e *Evaluator) message(rule Rule, target Target, value float64, state string, at time.Time) notify.AlertMessage {
	return notify.AlertMessage{
		Server:    target.Name,
		Metric:    MetricLabel(rule.Metric),
		Value:     FormatThresholdWithUnit(rule.Metric, value),
		Threshold: ComparatorSymbol(rule.Comparator) + " " + FormatThresholdWithUnit(rule.Metric, rule.Threshold),
		Severity:  rule.Severity,
		FiredAt:   at.UTC().Format("2006-01-02 15:04:05 UTC"),
		State:     state,
	}
}

func (e *Evaluator) incStreak(rule Rule, target Target) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	key := streakKey{ruleID: rule.ID, serverID: target.ID}
	e.streaks[key]++
	return e.streaks[key]
}

func (e *Evaluator) resetStreak(rule Rule, target Target) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.streaks, streakKey{ruleID: rule.ID, serverID: target.ID})
}

// ruleEventID returns the rule id to persist on an event. Rules always have a
// positive id here, but this keeps the nullable column explicit.
func ruleEventID(rule Rule) *int64 {
	if rule.ID == 0 {
		return nil
	}
	id := rule.ID
	return &id
}
