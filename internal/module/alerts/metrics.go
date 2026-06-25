package alerts

import (
	"strconv"
	"strings"
)

// Metric identifiers. These are the values persisted in alert_rules.metric and
// alert_silences.metric. Keep them in sync with the snapshot fields the
// evaluator reads in a later phase.
const (
	MetricCPU          = "cpu"
	MetricRAM          = "ram"
	MetricDisk         = "disk"
	MetricLoad1        = "load1"
	MetricLoad5        = "load5"
	MetricLoad15       = "load15"
	MetricTrafficTotal = "traffic_total"
	MetricBandwidth    = "bandwidth_mbps"

	// MetricServerUnreachable fires when a monitoring sweep cannot reach the
	// server (SSH/collection failed) — i.e. the server is down/offline. It is a
	// boolean (0/1) value like MetricProjectedExceedLimit, normalised to "≥ 1" in
	// validation, and is always available (so it both fires when down and resolves
	// when the server comes back). Unlike the observed metrics it needs no
	// snapshot, so it is evaluated even on the failure path of a sweep.
	MetricServerUnreachable = "server_unreachable"

	// Predictive (forecast-derived) metrics. Unlike the observed metrics above,
	// these come from the bandwidth forecast and warn BEFORE a limit is reached.
	// They are only available for servers that have a monthly RX limit configured
	// and enough history to project; otherwise the rule is skipped for the cycle
	// (see Metrics.valueFor and the scheduler's forecast wiring).
	//
	// MetricProjectedExceedLimit is a boolean (0/1) flag: 1 once the month-end RX
	// is projected to exceed the limit (or the limit is already exceeded). It is
	// modelled as a 0/1 value rather than a bespoke rule type so it reuses the
	// existing threshold/comparator/streak/cooldown machinery unchanged — its
	// comparator/threshold are normalised to "≥ 1" in validation.
	//
	// MetricDaysToExhaustion is the projected number of full days until the limit
	// is reached this month. It is "lower is worse": a rule fires when the days
	// remaining drop to or below the threshold (e.g. ≤ 3 days), so its comparator
	// is normalised to "≤" in validation.
	MetricProjectedExceedLimit = "projected_exceed_limit"
	MetricDaysToExhaustion     = "days_to_exhaustion"

	// MetricAll is only valid for silences: it mutes every metric for a server.
	MetricAll = "all"
)

// DaysToExhaustionSafe is the value reported for the days_to_exhaustion metric
// when a server has a limit configured but is NOT projected to reach it this
// month. It is deliberately far larger than any threshold validation accepts
// (capped well below it) so a "fire when days ≤ N" rule never breaches on a safe
// server. Crucially, reporting a large-but-available value (rather than marking
// the metric unavailable) lets an open event RESOLVE when traffic drops back to
// safe, instead of getting stuck because the metric vanished.
const DaysToExhaustionSafe = 100000

// maxDaysToExhaustionThreshold bounds the days_to_exhaustion threshold. A monthly
// cap resets at month-end, so days-to-limit is a within-month quantity (≤ 31);
// 60 is a generous ceiling that still rejects nonsensical values and stays well
// below DaysToExhaustionSafe.
const maxDaysToExhaustionThreshold = 60

// Comparators decide how an observed value is tested against the threshold. gt/gte
// are "higher is worse" (cpu, ram, traffic…); lt/lte are "lower is worse" and are
// used only by the predictive days_to_exhaustion metric.
const (
	ComparatorGT  = "gt"
	ComparatorGTE = "gte"
	ComparatorLT  = "lt"
	ComparatorLTE = "lte"
)

// Severities rank an alert from informational to critical.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// Channel kinds. Only Telegram exists today; the column is kept generic so
// webhook/email channels can be added later without a schema change.
const (
	ChannelKindTelegram = "telegram"
)

// ruleMetrics lists the metrics a rule may target, in display order.
var ruleMetrics = []string{
	MetricServerUnreachable,
	MetricCPU,
	MetricRAM,
	MetricDisk,
	MetricLoad1,
	MetricLoad5,
	MetricLoad15,
	MetricTrafficTotal,
	MetricBandwidth,
	MetricProjectedExceedLimit,
	MetricDaysToExhaustion,
}

var metricLabels = map[string]string{
	MetricServerUnreachable:    "Server unreachable",
	MetricCPU:                  "CPU usage",
	MetricRAM:                  "RAM usage",
	MetricDisk:                 "Disk usage",
	MetricLoad1:                "Load average (1m)",
	MetricLoad5:                "Load average (5m)",
	MetricLoad15:               "Load average (15m)",
	MetricTrafficTotal:         "Monthly traffic total",
	MetricBandwidth:            "Download bandwidth",
	MetricProjectedExceedLimit: "Projected to exceed monthly limit",
	MetricDaysToExhaustion:     "Days until monthly limit reached",
	MetricAll:                  "All metrics",
}

// metricUnits maps a metric to the unit its threshold is expressed in. An empty
// unit (load averages, the boolean projection) means the raw value carries no
// suffix.
var metricUnits = map[string]string{
	MetricCPU:              "%",
	MetricRAM:              "%",
	MetricDisk:             "%",
	MetricTrafficTotal:     "GiB",
	MetricBandwidth:        "Mbps",
	MetricDaysToExhaustion: "days",
}

// IsPredictiveMetric reports whether a metric is forecast-derived rather than an
// observed instantaneous value. Predictive metrics are gated on a configured
// monthly limit plus enough history, and use normalised comparators/thresholds.
func IsPredictiveMetric(metric string) bool {
	switch metric {
	case MetricProjectedExceedLimit, MetricDaysToExhaustion:
		return true
	default:
		return false
	}
}

// RuleMetrics returns the metrics that a rule may target.
func RuleMetrics() []string {
	out := make([]string, len(ruleMetrics))
	copy(out, ruleMetrics)
	return out
}

// SilenceMetrics returns the metrics a silence may target, including the "all"
// wildcard that mutes every metric for a server.
func SilenceMetrics() []string {
	return append(RuleMetrics(), MetricAll)
}

// IsRuleMetric reports whether metric is a valid target for an alert rule.
func IsRuleMetric(metric string) bool {
	_, ok := metricUnits[metric]
	if ok {
		return true
	}
	for _, candidate := range ruleMetrics {
		if candidate == metric {
			return true
		}
	}
	return false
}

// IsSilenceMetric reports whether metric is valid for a silence (rule metrics
// plus the "all" wildcard).
func IsSilenceMetric(metric string) bool {
	return metric == MetricAll || IsRuleMetric(metric)
}

// MetricLabel returns a human-readable label for a metric identifier.
func MetricLabel(metric string) string {
	if label, ok := metricLabels[metric]; ok {
		return label
	}
	return metric
}

// MetricUnit returns the unit suffix for a metric's threshold, or "" when the
// value is unitless (load averages).
func MetricUnit(metric string) string {
	return metricUnits[metric]
}

// IsComparator reports whether value is any supported comparator.
func IsComparator(value string) bool {
	switch value {
	case ComparatorGT, ComparatorGTE, ComparatorLT, ComparatorLTE:
		return true
	default:
		return false
	}
}

// isThresholdComparator reports whether value is a "higher is worse" comparator,
// the only kind valid for the observed metrics (cpu, ram, traffic…). The
// predictive days metric uses "lower is worse" comparators instead.
func isThresholdComparator(value string) bool {
	return value == ComparatorGT || value == ComparatorGTE
}

// IsSeverity reports whether value is a supported severity.
func IsSeverity(value string) bool {
	switch value {
	case SeverityInfo, SeverityWarning, SeverityCritical:
		return true
	default:
		return false
	}
}

// ComparatorSymbol renders a comparator as a math symbol for display.
func ComparatorSymbol(comparator string) string {
	switch comparator {
	case ComparatorGT:
		return ">"
	case ComparatorLT:
		return "<"
	case ComparatorLTE:
		return "≤"
	default:
		return "≥"
	}
}

// FormatThreshold renders a float threshold without trailing zeros.
func FormatThreshold(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// FormatThresholdWithUnit appends the metric's unit to a formatted threshold.
func FormatThresholdWithUnit(metric string, value float64) string {
	formatted := FormatThreshold(value)
	unit := MetricUnit(metric)
	if unit == "" {
		return formatted
	}
	if unit == "%" {
		return formatted + unit
	}
	return formatted + " " + unit
}

// Breaches reports whether an observed value breaches the threshold under the
// given comparator. Used by the evaluator in a later phase and kept here so the
// comparison lives next to the comparator constants.
func Breaches(comparator string, value, threshold float64) bool {
	switch comparator {
	case ComparatorGT:
		return value > threshold
	case ComparatorLT:
		return value < threshold
	case ComparatorLTE:
		return value <= threshold
	default: // gte
		return value >= threshold
	}
}

// normalizeMetric trims and lowercases a metric identifier.
func normalizeMetric(metric string) string {
	return strings.ToLower(strings.TrimSpace(metric))
}
