package alerts

import (
	"fmt"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/view"
)

// serverRef is the minimal server identity the alerts views need. The handler
// builds these from the servers repository so this module stays decoupled from
// the full server record.
type serverRef struct {
	ID   int64
	Name string
}

func serverNameMap(refs []serverRef) map[int64]string {
	names := make(map[int64]string, len(refs))
	for _, ref := range refs {
		names[ref.ID] = ref.Name
	}
	return names
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
	servers []serverRef,
	now time.Time,
) view.AlertsOverviewView {
	names := serverNameMap(servers)
	channelNames := channelNameMap(channels)

	return view.AlertsOverviewView{
		Rules:         buildRuleRows(rules, names, channelNames),
		Channels:      buildChannelRows(channels),
		Silences:      buildSilenceRows(silences, names, now),
		SilenceForm:   buildSilenceFormView(SilenceFormInput{}, ValidationErrors{}, servers),
		HasServers:    len(servers) > 0,
		NewRuleURL:    "/alerts/rules/new",
		NewChannelURL: "/alerts/channels/new",
	}
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

func buildRuleRows(rules []Rule, serverNames, channelNames map[int64]string) []view.AlertRuleView {
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

		rows = append(rows, view.AlertRuleView{
			ID:               rule.ID,
			ServerLabel:      serverLabel(serverNames, rule.ServerID),
			IsGlobal:         rule.IsGlobal(),
			Metric:           rule.Metric,
			MetricLabel:      MetricLabel(rule.Metric),
			ComparatorSymbol: ComparatorSymbol(rule.Comparator),
			ThresholdDisplay: FormatThresholdWithUnit(rule.Metric, rule.Threshold),
			ConsecutiveHits:  rule.ConsecutiveHits,
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
	return view.AlertRuleFormView{
		ID:              id,
		Metric:          input.Metric,
		Comparator:      input.Comparator,
		Threshold:       input.Threshold,
		ConsecutiveHits: input.ConsecutiveHits,
		CooldownSeconds: input.CooldownSeconds,
		Severity:        input.Severity,
		Enabled:         input.Enabled,
		Note:            input.Note,
		Action:          action,
		DeleteAction:    deleteAction,
		ServerOptions:   serverSelectOptions(servers, input.ServerID, true, "All servers (global rule)"),
		ChannelOptions:  channelSelectOptions(channels, input.ChannelID),
		MetricOptions:   metricSelectOptions(RuleMetrics(), input.Metric),
		Errors:          errs,
	}
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
