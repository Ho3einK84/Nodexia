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

	rule.Comparator = strings.ToLower(strings.TrimSpace(input.Comparator))
	if rule.Comparator == "" {
		rule.Comparator = ComparatorGTE
	} else if !IsComparator(rule.Comparator) {
		errs.Add("comparator", "Comparator must be gt or gte.")
	}

	rule.Severity = strings.ToLower(strings.TrimSpace(input.Severity))
	if rule.Severity == "" {
		rule.Severity = SeverityWarning
	} else if !IsSeverity(rule.Severity) {
		errs.Add("severity", "Severity must be info, warning, or critical.")
	}

	rule.Threshold = parseThreshold(strings.TrimSpace(input.Threshold), errs)
	rule.ConsecutiveHits = parseConsecutiveHits(strings.TrimSpace(input.ConsecutiveHits), errs)
	rule.CooldownSeconds = parseCooldown(strings.TrimSpace(input.CooldownSeconds), errs)

	rule.Note = strings.TrimSpace(input.Note)
	if len(rule.Note) > 500 {
		errs.Add("note", "Note must be 500 characters or fewer.")
	}

	return ValidatedRule{Rule: rule, Input: RuleFormInputFromRule(rule)}, errs
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
