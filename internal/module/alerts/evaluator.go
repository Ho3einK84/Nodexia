package alerts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	// Unreachable is true when the monitoring sweep could not reach the server
	// (SSH/collection failed). It drives MetricServerUnreachable and, because the
	// instantaneous values below are then unknown, marks every observed metric
	// unavailable so they are skipped — neither fired nor falsely resolved — for
	// the cycle. The zero value (false) means "reachable", so existing callers
	// that build Metrics from a successful snapshot keep working unchanged.
	Unreachable bool

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

	// NodeStatusAvailable gates MetricNodeStopped: it is true only on a node
	// discovery sweep that actually inspected the server's nodes. The monitoring
	// sweep leaves it false (it carries no node data), so node_stopped rules are
	// skipped there — neither fired nor falsely resolved — exactly like the
	// traffic/forecast availability gates. NodeStopped is true when at least one
	// discovered node is in the "stopped" state.
	NodeStatusAvailable bool
	NodeStopped         bool

	// ForecastAvailable gates the predictive (forecast-derived) metrics. It is
	// true only when the server has a traffic limit configured AND there is
	// enough traffic history to project. When false, projected_exceed_limit and
	// days_to_exhaustion rules are skipped this cycle — neither fired nor
	// resolved — exactly like an unavailable traffic snapshot. This keeps streaks
	// and cooldowns sane across availability gaps and guarantees servers without a
	// limit never trigger the predictive metrics.
	ForecastAvailable     bool
	ProjectedExceedsLimit bool    // period-end usage projected over the limit (or already over)
	DaysToExhaustion      float64 // full days until the limit is reached; DaysToExhaustionSafe when not projected to exhaust

	// AnomalyAvailable gates the forecast anomaly metrics (traffic_spike,
	// unusual_growth). Unlike ForecastAvailable it needs NO configured limit —
	// only traffic history this cycle — so anomaly rules work on any server
	// vnStat covers. When false the anomaly rules are skipped, keeping streaks
	// and cooldowns sane across gaps.
	AnomalyAvailable bool
	TrafficSpike     bool // today's predicted rate ≥ 2x the historical daily average
	UnusualGrowth    bool // projected period total > 1.5x the previous 30 completed days
}

// valueFor returns the observed value for a metric and whether it is available
// this cycle.
func (m Metrics) valueFor(metric string) (float64, bool) {
	// reachable is the availability gate for every observed metric: when the
	// server is unreachable its CPU/RAM/traffic values are unknown, so those rules
	// are skipped (and only server_unreachable fires).
	reachable := !m.Unreachable
	switch metric {
	case MetricServerUnreachable:
		if m.Unreachable {
			return 1, true
		}
		return 0, true
	case MetricNodeStopped:
		if !m.NodeStatusAvailable {
			return 0, false
		}
		if m.NodeStopped {
			return 1, true
		}
		return 0, true
	case MetricCPU:
		return m.CPU, reachable
	case MetricRAM:
		return m.RAM, reachable
	case MetricDisk:
		return m.Disk, reachable
	case MetricLoad1:
		return m.Load1, reachable
	case MetricLoad5:
		return m.Load5, reachable
	case MetricLoad15:
		return m.Load15, reachable
	case MetricTrafficTotal:
		return m.TrafficTotalGiB, reachable && m.TrafficAvailable
	case MetricBandwidth:
		// bandwidth_mbps alerts on the peak download (RX) sample for the
		// period. Upload (TX) is excluded so the threshold lines up with the
		// download-only port speed VPS providers advertise.
		return m.PeakMbps, reachable && m.TrafficAvailable
	case MetricProjectedExceedLimit:
		if !m.ForecastAvailable {
			return 0, false
		}
		if m.ProjectedExceedsLimit {
			return 1, true
		}
		return 0, true
	case MetricDaysToExhaustion:
		return m.DaysToExhaustion, m.ForecastAvailable
	case MetricTrafficSpike:
		if !m.AnomalyAvailable {
			return 0, false
		}
		if m.TrafficSpike {
			return 1, true
		}
		return 0, true
	case MetricUnusualGrowth:
		if !m.AnomalyAvailable {
			return 0, false
		}
		if m.UnusualGrowth {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

// Evaluator turns collected metrics into firing/resolved alert events and
// dispatches notifications. All state — events, cooldowns, and consecutive-breach
// streaks — is persisted to the database so it survives restarts. The streaks
// table (alert_rule_streaks) is updated on every evaluation cycle.
type Evaluator struct {
	repo     Repository
	notifier notify.Notifier // nil when no Telegram token is configured
	clock    func() time.Time
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
		slog.Debug("alert: metric unavailable this cycle — skipping rule",
			slog.Int64("rule_id", rule.ID),
			slog.Int64("server_id", target.ID),
			slog.String("metric", rule.Metric),
		)
		return nil
	}

	now := e.clock()
	breach := Breaches(rule.Comparator, value, rule.Threshold)

	slog.Debug("alert: evaluating rule",
		slog.Int64("rule_id", rule.ID),
		slog.Int64("server_id", target.ID),
		slog.String("server", target.Name),
		slog.String("metric", rule.Metric),
		slog.Float64("value", value),
		slog.Float64("threshold", rule.Threshold),
		slog.String("comparator", rule.Comparator),
		slog.Bool("breach", breach),
	)

	open, openErr := e.repo.GetOpenEvent(ctx, rule.ID, target.ID)
	hasOpen := openErr == nil
	if openErr != nil && !errors.Is(openErr, ErrNotFound) {
		return openErr
	}

	// Recovery: value is back below the threshold. Resolve any open event,
	// regardless of silence (a silence suppresses firing, not resolution).
	if !breach {
		if err := e.resetStreak(ctx, rule, target); err != nil {
			return err
		}
		if hasOpen {
			slog.Info("alert: metric recovered — resolving open event",
				slog.Int64("rule_id", rule.ID),
				slog.Int64("server_id", target.ID),
				slog.String("metric", rule.Metric),
				slog.Float64("value", value),
			)
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
		slog.Debug("alert: breach suppressed by silence",
			slog.Int64("rule_id", rule.ID),
			slog.Int64("server_id", target.ID),
			slog.String("metric", rule.Metric),
		)
		if err := e.resetStreak(ctx, rule, target); err != nil {
			return err
		}
		return nil
	}

	if hasOpen {
		// Already firing: re-notify only once the cooldown has elapsed.
		if e.cooldownElapsed(rule, open, now) {
			slog.Info("alert: cooldown elapsed — re-notifying",
				slog.Int64("rule_id", rule.ID),
				slog.Int64("server_id", target.ID),
				slog.String("metric", rule.Metric),
			)
			e.dispatch(ctx, rule, target, e.message(rule, target, value, notify.StateFiring, now))
			if err := e.repo.MarkEventNotified(ctx, open.ID, now); err != nil {
				return err
			}
		} else {
			slog.Debug("alert: already firing, cooldown not yet elapsed",
				slog.Int64("rule_id", rule.ID),
				slog.Int64("server_id", target.ID),
				slog.String("metric", rule.Metric),
				slog.Int("cooldown_seconds", rule.CooldownSeconds),
			)
		}
		return nil
	}

	// Not yet firing: accumulate consecutive breaches before transitioning.
	streak, err := e.incStreak(ctx, rule, target)
	if err != nil {
		return err
	}
	if streak < rule.ConsecutiveHits {
		slog.Info("alert: breach detected — waiting for consecutive hits",
			slog.Int64("rule_id", rule.ID),
			slog.Int64("server_id", target.ID),
			slog.String("server", target.Name),
			slog.String("metric", rule.Metric),
			slog.Float64("value", value),
			slog.Int("streak", streak),
			slog.Int("required", rule.ConsecutiveHits),
		)
		return nil
	}
	slog.Info("alert: firing",
		slog.Int64("rule_id", rule.ID),
		slog.Int64("server_id", target.ID),
		slog.String("server", target.Name),
		slog.String("metric", rule.Metric),
		slog.Float64("value", value),
		slog.Int("consecutive_hits", rule.ConsecutiveHits),
	)
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

	if err := e.resetStreak(ctx, rule, target); err != nil {
		return err
	}
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

func (e *Evaluator) incStreak(ctx context.Context, rule Rule, target Target) (int, error) {
	return e.repo.IncrementStreak(ctx, rule.ID, target.ID)
}

func (e *Evaluator) resetStreak(ctx context.Context, rule Rule, target Target) error {
	return e.repo.SetStreak(ctx, rule.ID, target.ID, 0)
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
