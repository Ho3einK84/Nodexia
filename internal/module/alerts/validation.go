package alerts

import (
	"strconv"
	"strings"
	"time"
)

// ValidationErrors maps a form field name to a human-readable error message. It
// mirrors the servers module so templates can render field-level errors the
// same way.
type ValidationErrors map[string]string

func (v ValidationErrors) Add(field, message string) {
	if _, exists := v[field]; exists {
		return
	}
	v[field] = message
}

func (v ValidationErrors) HasAny() bool {
	return len(v) > 0
}

// ── Rule form ────────────────────────────────────────────────────────────────

// RuleFormInput is the raw, string-based representation of the rule form. An
// empty ServerID or ChannelID means "all" (global rule / every channel).
type RuleFormInput struct {
	ServerID        string
	Metric          string
	Comparator      string
	Threshold       string
	ConsecutiveHits string
	CooldownSeconds string
	Severity        string
	ChannelID       string
	Enabled         bool
	Note            string
}

type ValidatedRule struct {
	Rule  Rule
	Input RuleFormInput
}

// DefaultRuleFormInput returns the prefilled values shown on the new-rule form.
func DefaultRuleFormInput() RuleFormInput {
	return RuleFormInput{
		Metric:          MetricCPU,
		Comparator:      ComparatorGTE,
		Threshold:       "90",
		ConsecutiveHits: "1",
		CooldownSeconds: "900",
		Severity:        SeverityWarning,
		Enabled:         true,
	}
}

// RuleFormInputFromRule echoes a persisted rule back into form input.
func RuleFormInputFromRule(rule Rule) RuleFormInput {
	return RuleFormInput{
		ServerID:        optionalID(rule.ServerID),
		Metric:          rule.Metric,
		Comparator:      rule.Comparator,
		Threshold:       FormatThreshold(rule.Threshold),
		ConsecutiveHits: strconv.Itoa(rule.ConsecutiveHits),
		CooldownSeconds: strconv.Itoa(rule.CooldownSeconds),
		Severity:        rule.Severity,
		ChannelID:       optionalID(rule.ChannelID),
		Enabled:         rule.Enabled,
		Note:            rule.Note,
	}
}

func ValidateRuleForm(input RuleFormInput) (ValidatedRule, ValidationErrors) {
	errs := ValidationErrors{}
	rule := Rule{Enabled: input.Enabled}

	rule.ServerID = parseOptionalID(strings.TrimSpace(input.ServerID), "server_id", errs)
	rule.ChannelID = parseOptionalID(strings.TrimSpace(input.ChannelID), "channel_id", errs)

	rule.Metric = normalizeMetric(input.Metric)
	if rule.Metric == "" {
		errs.Add("metric", "Metric is required.")
	} else if !IsRuleMetric(rule.Metric) {
		errs.Add("metric", "Metric is not supported.")
	}

	rule.Severity = strings.ToLower(strings.TrimSpace(input.Severity))
	if rule.Severity == "" {
		rule.Severity = SeverityWarning
	} else if !IsSeverity(rule.Severity) {
		errs.Add("severity", "Severity must be info, warning, or critical.")
	}

	// Comparator + threshold are metric-aware: the predictive metrics normalise
	// them to coherent fixed/forced values (see applyRuleCondition).
	applyRuleCondition(&rule, input, errs)
	rule.ConsecutiveHits = parseConsecutiveHits(strings.TrimSpace(input.ConsecutiveHits), errs)
	rule.CooldownSeconds = parseCooldown(strings.TrimSpace(input.CooldownSeconds), errs)

	rule.Note = strings.TrimSpace(input.Note)
	if len(rule.Note) > 500 {
		errs.Add("note", "Note must be 500 characters or fewer.")
	}

	return ValidatedRule{Rule: rule, Input: RuleFormInputFromRule(rule)}, errs
}

// applyRuleCondition resolves a rule's comparator and threshold from the form,
// honouring each metric's semantics. Observed metrics (cpu, ram, traffic…) keep
// a "higher is worse" comparator plus a free threshold; the boolean projection
// is forced to "≥ 1"; the days-to-limit metric is forced to "≤" with a bounded
// day count. Forcing the predictive comparators means the operator only has to
// pick the metric (and, for the days metric, the day count) and can never store
// an incoherent condition such as "cpu ≤ 90" or "days ≥ 3".
func applyRuleCondition(rule *Rule, input RuleFormInput, errs ValidationErrors) {
	comparator := strings.ToLower(strings.TrimSpace(input.Comparator))
	threshold := strings.TrimSpace(input.Threshold)

	switch rule.Metric {
	case MetricProjectedExceedLimit:
		rule.Comparator = ComparatorGTE
		rule.Threshold = 1
	case MetricDaysToExhaustion:
		rule.Comparator = ComparatorLTE
		rule.Threshold = parseDaysThreshold(threshold, errs)
	default:
		if comparator == "" {
			rule.Comparator = ComparatorGTE
		} else {
			rule.Comparator = comparator
			if !isThresholdComparator(comparator) {
				errs.Add("comparator", "Comparator must be gt or gte.")
			}
		}
		rule.Threshold = parseThreshold(threshold, errs)
	}
}

// parseDaysThreshold validates the days_to_exhaustion threshold: a number of days
// in [0, maxDaysToExhaustionThreshold]. A monthly cap resets at month-end, so a
// days-to-limit value above ~31 is meaningless; the bound rejects nonsensical
// thresholds while staying far below DaysToExhaustionSafe.
func parseDaysThreshold(value string, errs ValidationErrors) float64 {
	if value == "" {
		errs.Add("threshold", "Enter the days-remaining value that should trigger the alert.")
		return 0
	}
	days, err := strconv.ParseFloat(value, 64)
	if err != nil {
		errs.Add("threshold", "Days threshold must be a number.")
		return 0
	}
	if days < 0 {
		errs.Add("threshold", "Days threshold must be zero or greater.")
		return 0
	}
	if days > maxDaysToExhaustionThreshold {
		errs.Add("threshold", "Days threshold must be 60 or fewer — a monthly limit resets at month-end.")
		return 0
	}
	return days
}

func parseThreshold(value string, errs ValidationErrors) float64 {
	if value == "" {
		errs.Add("threshold", "Threshold is required.")
		return 0
	}
	threshold, err := strconv.ParseFloat(value, 64)
	if err != nil {
		errs.Add("threshold", "Threshold must be a number.")
		return 0
	}
	if threshold < 0 {
		errs.Add("threshold", "Threshold must be zero or greater.")
		return 0
	}
	return threshold
}

func parseConsecutiveHits(value string, errs ValidationErrors) int {
	if value == "" {
		return 1
	}
	hits, err := strconv.Atoi(value)
	if err != nil {
		errs.Add("consecutive_hits", "Consecutive hits must be a whole number.")
		return 1
	}
	if hits < 1 {
		errs.Add("consecutive_hits", "Consecutive hits must be at least 1.")
		return 1
	}
	return hits
}

func parseCooldown(value string, errs ValidationErrors) int {
	if value == "" {
		return 900
	}
	cooldown, err := strconv.Atoi(value)
	if err != nil {
		errs.Add("cooldown_seconds", "Cooldown must be a whole number of seconds.")
		return 900
	}
	if cooldown < 0 {
		errs.Add("cooldown_seconds", "Cooldown must be zero or greater.")
		return 900
	}
	return cooldown
}

// ── Channel form ─────────────────────────────────────────────────────────────

type ChannelFormInput struct {
	Kind            string
	Name            string
	ChatID          string
	MessageTemplate string
	Enabled         bool
}

type ValidatedChannel struct {
	Channel Channel
	Input   ChannelFormInput
}

func DefaultChannelFormInput() ChannelFormInput {
	return ChannelFormInput{
		Kind:    ChannelKindTelegram,
		Enabled: true,
	}
}

func ChannelFormInputFromChannel(channel Channel) ChannelFormInput {
	return ChannelFormInput{
		Kind:            channel.Kind,
		Name:            channel.Name,
		ChatID:          channel.ChatID,
		MessageTemplate: channel.MessageTemplate,
		Enabled:         channel.Enabled,
	}
}

func ValidateChannelForm(input ChannelFormInput) (ValidatedChannel, ValidationErrors) {
	errs := ValidationErrors{}
	channel := Channel{Enabled: input.Enabled}

	channel.Kind = strings.ToLower(strings.TrimSpace(input.Kind))
	if channel.Kind == "" {
		channel.Kind = ChannelKindTelegram
	} else if channel.Kind != ChannelKindTelegram {
		errs.Add("kind", "Only the telegram channel kind is supported.")
	}

	channel.Name = strings.TrimSpace(input.Name)
	if channel.Name == "" {
		errs.Add("name", "Channel name is required.")
	} else if len(channel.Name) > 120 {
		errs.Add("name", "Channel name must be 120 characters or fewer.")
	}

	channel.ChatID = strings.TrimSpace(input.ChatID)
	if channel.Kind == ChannelKindTelegram && channel.ChatID == "" {
		errs.Add("chat_id", "Chat id is required for a Telegram channel.")
	}
	if len(channel.ChatID) > 120 {
		errs.Add("chat_id", "Chat id must be 120 characters or fewer.")
	}

	channel.MessageTemplate = strings.TrimSpace(input.MessageTemplate)
	if len(channel.MessageTemplate) > 2000 {
		errs.Add("message_template", "Message template must be 2000 characters or fewer.")
	}

	return ValidatedChannel{Channel: channel, Input: ChannelFormInputFromChannel(channel)}, errs
}

// ── Silence form ─────────────────────────────────────────────────────────────

type SilenceFormInput struct {
	ServerID     string
	Metric       string
	Reason       string
	ExpiresHours string
}

type ValidatedSilence struct {
	Silence Silence
	Input   SilenceFormInput
}

// ValidateSilenceForm validates a silence. now anchors the relative expiry so
// callers (and tests) control the clock.
func ValidateSilenceForm(input SilenceFormInput, now time.Time) (ValidatedSilence, ValidationErrors) {
	errs := ValidationErrors{}
	silence := Silence{}

	serverID, err := strconv.ParseInt(strings.TrimSpace(input.ServerID), 10, 64)
	if err != nil || serverID < 1 {
		errs.Add("server_id", "Select a server to silence.")
	} else {
		silence.ServerID = serverID
	}

	silence.Metric = normalizeMetric(input.Metric)
	if silence.Metric == "" {
		errs.Add("metric", "Metric is required.")
	} else if !IsSilenceMetric(silence.Metric) {
		errs.Add("metric", "Metric is not supported.")
	}

	silence.Reason = strings.TrimSpace(input.Reason)
	if len(silence.Reason) > 255 {
		errs.Add("reason", "Reason must be 255 characters or fewer.")
	}

	if hours := strings.TrimSpace(input.ExpiresHours); hours != "" {
		parsed, err := strconv.ParseFloat(hours, 64)
		if err != nil {
			errs.Add("expires_hours", "Expiry must be a number of hours.")
		} else if parsed < 0 {
			errs.Add("expires_hours", "Expiry must be zero or greater.")
		} else if parsed > 0 {
			expiresAt := now.UTC().Add(time.Duration(parsed * float64(time.Hour)))
			silence.ExpiresAt = &expiresAt
		}
	}

	return ValidatedSilence{Silence: silence, Input: input}, errs
}

// parseOptionalID parses an optional foreign-key form value. Empty, "0", "all",
// or "global" all resolve to nil (no association). An invalid value records a
// field error and returns nil.
func parseOptionalID(value, field string, errs ValidationErrors) *int64 {
	switch strings.ToLower(value) {
	case "", "0", "all", "global":
		return nil
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id < 1 {
		errs.Add(field, "Selection is invalid.")
		return nil
	}
	return &id
}

func optionalID(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}
