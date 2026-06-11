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

	// MetricAll is only valid for silences: it mutes every metric for a server.
	MetricAll = "all"
)

// Comparators decide how an observed value is tested against the threshold.
const (
	ComparatorGT  = "gt"
	ComparatorGTE = "gte"
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
	MetricCPU,
	MetricRAM,
	MetricDisk,
	MetricLoad1,
	MetricLoad5,
	MetricLoad15,
	MetricTrafficTotal,
	MetricBandwidth,
}

var metricLabels = map[string]string{
	MetricCPU:          "CPU usage",
	MetricRAM:          "RAM usage",
	MetricDisk:         "Disk usage",
	MetricLoad1:        "Load average (1m)",
	MetricLoad5:        "Load average (5m)",
	MetricLoad15:       "Load average (15m)",
	MetricTrafficTotal: "Monthly traffic total",
	MetricBandwidth:    "Bandwidth",
	MetricAll:          "All metrics",
}

// metricUnits maps a metric to the unit its threshold is expressed in. An empty
// unit (load averages) means the raw value carries no suffix.
var metricUnits = map[string]string{
	MetricCPU:          "%",
	MetricRAM:          "%",
	MetricDisk:         "%",
	MetricTrafficTotal: "GiB",
	MetricBandwidth:    "Mbps",
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

// IsComparator reports whether value is a supported comparator.
func IsComparator(value string) bool {
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
	if comparator == ComparatorGT {
		return ">"
	}
	return "≥" // ≥
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
	if comparator == ComparatorGT {
		return value > threshold
	}
	return value >= threshold
}

// normalizeMetric trims and lowercases a metric identifier.
func normalizeMetric(metric string) string {
	return strings.ToLower(strings.TrimSpace(metric))
}
