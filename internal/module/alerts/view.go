package alerts

import (
	"fmt"
	"strconv"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/geoip"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

// serverRef is the minimal server identity the alerts views need. The handler
// builds these from the servers repository so this module stays decoupled from
// the full server record. CountryCode is the detected ISO 3166-1 alpha-2 code
// (or "" when unknown) used to render a flag badge next to the server.
type serverRef struct {
	ID          int64
	Name        string
	CountryCode string
}

func serverNameMap(refs []serverRef) map[int64]string {
	names := make(map[int64]string, len(refs))
	for _, ref := range refs {
		names[ref.ID] = ref.Name
	}
	return names
}

// serverCountryMap indexes each server's detected country code by id so event
// rows can render a flag without an extra lookup per event.
func serverCountryMap(refs []serverRef) map[int64]string {
	codes := make(map[int64]string, len(refs))
	for _, ref := range refs {
		codes[ref.ID] = ref.CountryCode
	}
	return codes
}

func channelNameMap(channels []Channel) map[int64]string {
	names := make(map[int64]string, len(channels))
	for _, channel := range channels {
		names[channel.ID] = channel.Name
	}
	return names
}

// ── Overview ─────────────────────────────────────────────────────────────────

func buildOverview(
	rules []Rule,
	channels []Channel,
	silences []Silence,
	events []Event,
	servers []serverRef,
	streaks map[streakKey]int,
	tokenConfigured bool,
	now time.Time,
) view.AlertsOverviewView {
	names := serverNameMap(servers)
	countries := serverCountryMap(servers)
	channelNames := channelNameMap(channels)

	notice := ""
	if !tokenConfigured {
		notice = "Telegram bot token not configured — set NODEXIA_TELEGRAM_BOT_TOKEN to enable sending test messages and alerts."
	}

	return view.AlertsOverviewView{
		Rules:           buildRuleRows(rules, names, channelNames, streaks),
		Channels:        buildChannelRows(channels),
		Silences:        buildSilenceRows(silences, names, now),
		Events:          buildEventRows(events, names, countries),
		SilenceForm:     buildSilenceFormView(SilenceFormInput{}, ValidationErrors{}, servers),
		HasServers:      len(servers) > 0,
		NewRuleURL:      "/alerts/rules/new",
		NewChannelURL:   "/alerts/channels/new",
		TokenConfigured: tokenConfigured,
		TokenNotice:     notice,
	}
}

func buildEventRows(events []Event, serverNames, serverCountries map[int64]string) []view.AlertEventView {
	rows := make([]view.AlertEventView, 0, len(events))
	for _, event := range events {
		serverID := event.ServerID
		resolvedAt := ""
		if event.ResolvedAt != nil {
			resolvedAt = formatTimestamp(*event.ResolvedAt)
		}

		code := serverCountries[serverID]
		rows = append(rows, view.AlertEventView{
			ServerLabel: serverLabel(serverNames, &serverID),
			FlagEmoji:   geoip.FlagEmoji(code),
			CountryName: geoip.CountryName(code),
			Metric:      event.Metric,
			MetricLabel: MetricLabel(event.Metric),
			Value:       FormatThresholdWithUnit(event.Metric, event.ObservedValue),
			Threshold:   FormatThresholdWithUnit(event.Metric, event.Threshold),
			Severity:    event.Severity,
			State:       event.State,
			FiredAt:     formatTimestamp(event.FiredAt),
			ResolvedAt:  resolvedAt,
		})
	}
	return rows
}

func buildSilenceFormView(input SilenceFormInput, errs ValidationErrors, servers []serverRef) view.AlertSilenceFormView {
	return view.AlertSilenceFormView{
		Action:        "/alerts/silences",
		Reason:        input.Reason,
		ExpiresHours:  input.ExpiresHours,
		ServerOptions: serverSelectOptions(servers, input.ServerID, false, ""),
		MetricOptions: metricSelectOptions(SilenceMetrics(), input.Metric),
		Errors:        errs,
	}
}

func buildRuleRows(rules []Rule, serverNames, channelNames map[int64]string, streaks map[streakKey]int) []view.AlertRuleView {
	rows := make([]view.AlertRuleView, 0, len(rules))
	for _, rule := range rules {
		channelLabel := "All enabled channels"
		if rule.ChannelID != nil {
			if name, ok := channelNames[*rule.ChannelID]; ok {
				channelLabel = name
			} else {
				channelLabel = fmt.Sprintf("Channel #%d", *rule.ChannelID)
			}
		}

		// Compute streak summary: show "N/M" when consecutive breaches are
		// accumulating but the threshold hasn't been reached yet, so operators
		// can see the rule is progressing toward firing.
		streakSummary := ""
		if rule.ConsecutiveHits > 1 {
			maxStreak := 0
			if rule.ServerID != nil {
				// Server-specific rule: only one possible server.
				if s, ok := streaks[streakKey{ruleID: rule.ID, serverID: *rule.ServerID}]; ok && s > 0 {
					maxStreak = s
				}
			} else {
				// Global rule: find the highest streak across all servers.
				for k, s := range streaks {
					if k.ruleID == rule.ID && s > maxStreak {
						maxStreak = s
					}
				}
			}
			if maxStreak > 0 {
				streakSummary = strconv.Itoa(maxStreak) + "/" + strconv.Itoa(rule.ConsecutiveHits)
			}
		}

		rows = append(rows, view.AlertRuleView{
			ID:               rule.ID,
			ServerLabel:      serverLabel(serverNames, rule.ServerID),
			IsGlobal:         rule.IsGlobal(),
			Metric:           rule.Metric,
			MetricLabel:      MetricLabel(rule.Metric),
			ConditionKind:    MetricKind(rule.Metric),
			ComparatorSymbol: ComparatorSymbol(rule.Comparator),
			ThresholdDisplay: FormatThresholdWithUnit(rule.Metric, rule.Threshold),
			ConsecutiveHits:  rule.ConsecutiveHits,
			StreakSummary:    streakSummary,
			Cooldown:         humanizeDurationSeconds(rule.CooldownSeconds),
			Severity:         rule.Severity,
			ChannelLabel:     channelLabel,
			Enabled:          rule.Enabled,
			Note:             rule.Note,
			EditURL:          "/alerts/rules/" + formatID(rule.ID) + "/edit",
			DeleteURL:        "/alerts/rules/" + formatID(rule.ID) + "/delete",
		})
	}
	return rows
}

func buildChannelRows(channels []Channel) []view.AlertChannelView {
	rows := make([]view.AlertChannelView, 0, len(channels))
	for _, channel := range channels {
		rows = append(rows, view.AlertChannelView{
			ID:          channel.ID,
			Kind:        channel.Kind,
			Name:        channel.Name,
			ChatID:      channel.ChatID,
			HasTemplate: channel.MessageTemplate != "",
			Enabled:     channel.Enabled,
			EditURL:     "/alerts/channels/" + formatID(channel.ID) + "/edit",
			DeleteURL:   "/alerts/channels/" + formatID(channel.ID) + "/delete",
			TestURL:     "/alerts/channels/" + formatID(channel.ID) + "/test",
		})
	}
	return rows
}

func buildSilenceRows(silences []Silence, serverNames map[int64]string, now time.Time) []view.AlertSilenceView {
	rows := make([]view.AlertSilenceView, 0, len(silences))
	for _, silence := range silences {
		serverID := silence.ServerID
		expires := "Never"
		if silence.ExpiresAt != nil {
			expires = formatTimestamp(*silence.ExpiresAt)
		}

		rows = append(rows, view.AlertSilenceView{
			ID:          silence.ID,
			ServerLabel: serverLabel(serverNames, &serverID),
			Metric:      silence.Metric,
			MetricLabel: MetricLabel(silence.Metric),
			Reason:      silence.Reason,
			Expires:     expires,
			Active:      silence.IsActive(now),
			DeleteURL:   "/alerts/silences/" + formatID(silence.ID) + "/delete",
		})
	}
	return rows
}

// ── Forms ────────────────────────────────────────────────────────────────────

func buildRuleFormView(
	id int64,
	input RuleFormInput,
	errs ValidationErrors,
	action, deleteAction string,
	servers []serverRef,
	channels []Channel,
) view.AlertRuleFormView {
	metrics, selected := ruleMetricOptions(input.Metric)
	return view.AlertRuleFormView{
		ID:                  id,
		Metric:              input.Metric,
		Comparator:          input.Comparator,
		Threshold:           input.Threshold,
		ConsecutiveHits:     input.ConsecutiveHits,
		CooldownSeconds:     input.CooldownSeconds,
		Severity:            input.Severity,
		Enabled:             input.Enabled,
		Note:                input.Note,
		Action:              action,
		DeleteAction:        deleteAction,
		ServerOptions:       serverSelectOptions(servers, input.ServerID, true, "All servers (global rule)"),
		ChannelOptions:      channelSelectOptions(channels, input.ChannelID),
		Metrics:             metrics,
		SelectedMetric:      selected,
		CooldownIsPreset:    isCooldownPreset(input.CooldownSeconds),
		CooldownCustomLabel: cooldownCustomLabel(input.CooldownSeconds),
		Errors:              errs,
	}
}

// cooldownPresets are the cooldown choices the rule form offers, in seconds.
// Keep in sync with the <select> options in content-alert-rule-form.
var cooldownPresets = []string{"0", "300", "900", "1800", "3600", "21600", "86400"}

func isCooldownPreset(value string) bool {
	for _, preset := range cooldownPresets {
		if value == preset {
			return true
		}
	}
	return false
}

// cooldownCustomLabel humanizes a non-preset cooldown so an edited rule shows
// its stored value (e.g. "1h 30m") instead of a bare seconds count.
func cooldownCustomLabel(value string) string {
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 0 {
		return value
	}
	return humanizeDurationSeconds(seconds)
}

// ruleMetricOptions assembles the metric picker entries plus the currently
// selected one (falling back to the first metric) so the template can render
// the matching condition controls server-side.
func ruleMetricOptions(selected string) ([]view.AlertMetricOptionView, view.AlertMetricOptionView) {
	options := make([]view.AlertMetricOptionView, 0, len(ruleMetrics))
	var current view.AlertMetricOptionView
	for _, metric := range ruleMetrics {
		option := view.AlertMetricOptionView{
			Value:            metric,
			Kind:             MetricKind(metric),
			Unit:             MetricUnit(metric),
			DefaultThreshold: MetricDefaultThreshold(metric),
			NeedsLimit:       IsPredictiveMetric(metric),
			NeedsHistory:     IsAnomalyMetric(metric),
			Selected:         metric == selected,
		}
		if option.Selected {
			current = option
		}
		options = append(options, option)
	}
	if current.Value == "" && len(options) > 0 {
		options[0].Selected = true
		current = options[0]
	}
	return options, current
}

func buildChannelFormView(id int64, input ChannelFormInput, errs ValidationErrors, action, deleteAction string) view.AlertChannelFormView {
	return view.AlertChannelFormView{
		ID:              id,
		Kind:            input.Kind,
		Name:            input.Name,
		ChatID:          input.ChatID,
		MessageTemplate: input.MessageTemplate,
		Enabled:         input.Enabled,
		Action:          action,
		DeleteAction:    deleteAction,
		Errors:          errs,
	}
}

func serverSelectOptions(servers []serverRef, selected string, includeAll bool, allLabel string) []view.AlertOptionView {
	options := make([]view.AlertOptionView, 0, len(servers)+1)
	if includeAll {
		options = append(options, view.AlertOptionView{
			Value:    "",
			Label:    allLabel,
			Selected: selected == "" || selected == "0",
		})
	}
	for _, srv := range servers {
		value := formatID(srv.ID)
		options = append(options, view.AlertOptionView{
			Value:    value,
			Label:    fmt.Sprintf("%s (#%d)", srv.Name, srv.ID),
			Selected: value == selected,
		})
	}
	return options
}

func channelSelectOptions(channels []Channel, selected string) []view.AlertOptionView {
	options := make([]view.AlertOptionView, 0, len(channels)+1)
	options = append(options, view.AlertOptionView{
		Value:    "",
		Label:    "All enabled channels",
		Selected: selected == "" || selected == "0",
	})
	for _, channel := range channels {
		value := formatID(channel.ID)
		label := channel.Name
		if !channel.Enabled {
			label += " (disabled)"
		}
		options = append(options, view.AlertOptionView{
			Value:    value,
			Label:    label,
			Selected: value == selected,
		})
	}
	return options
}

func metricSelectOptions(metrics []string, selected string) []view.AlertOptionView {
	options := make([]view.AlertOptionView, 0, len(metrics))
	for _, metric := range metrics {
		options = append(options, view.AlertOptionView{
			Value:    metric,
			Label:    MetricLabel(metric),
			Selected: metric == selected,
		})
	}
	return options
}
