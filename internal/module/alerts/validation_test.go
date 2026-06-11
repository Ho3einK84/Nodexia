package alerts

import (
	"testing"
	"time"
)

func TestValidateRuleFormAcceptsGlobalRule(t *testing.T) {
	validated, errs := ValidateRuleForm(RuleFormInput{
		ServerID:        "",
		Metric:          MetricCPU,
		Comparator:      ComparatorGTE,
		Threshold:       "90",
		ConsecutiveHits: "3",
		CooldownSeconds: "900",
		Severity:        SeverityWarning,
		ChannelID:       "",
		Enabled:         true,
	})

	if errs.HasAny() {
		t.Fatalf("unexpected validation errors: %#v", errs)
	}
	if validated.Rule.ServerID != nil {
		t.Fatalf("ServerID = %v, want nil (global)", validated.Rule.ServerID)
	}
	if validated.Rule.ChannelID != nil {
		t.Fatalf("ChannelID = %v, want nil (all channels)", validated.Rule.ChannelID)
	}
	if validated.Rule.Threshold != 90 {
		t.Fatalf("Threshold = %v, want 90", validated.Rule.Threshold)
	}
	if validated.Rule.ConsecutiveHits != 3 {
		t.Fatalf("ConsecutiveHits = %d, want 3", validated.Rule.ConsecutiveHits)
	}
	if !validated.Rule.IsGlobal() {
		t.Fatal("expected rule to be global")
	}
}

func TestValidateRuleFormAcceptsServerScopedRule(t *testing.T) {
	validated, errs := ValidateRuleForm(RuleFormInput{
		ServerID:  "7",
		Metric:    MetricTrafficTotal,
		Threshold: "500",
		ChannelID: "3",
		Enabled:   true,
	})

	if errs.HasAny() {
		t.Fatalf("unexpected validation errors: %#v", errs)
	}
	if validated.Rule.ServerID == nil || *validated.Rule.ServerID != 7 {
		t.Fatalf("ServerID = %v, want 7", validated.Rule.ServerID)
	}
	if validated.Rule.ChannelID == nil || *validated.Rule.ChannelID != 3 {
		t.Fatalf("ChannelID = %v, want 3", validated.Rule.ChannelID)
	}
	// Defaults applied when omitted.
	if validated.Rule.Comparator != ComparatorGTE {
		t.Fatalf("Comparator = %q, want default gte", validated.Rule.Comparator)
	}
	if validated.Rule.Severity != SeverityWarning {
		t.Fatalf("Severity = %q, want default warning", validated.Rule.Severity)
	}
	if validated.Rule.ConsecutiveHits != 1 {
		t.Fatalf("ConsecutiveHits = %d, want default 1", validated.Rule.ConsecutiveHits)
	}
	if validated.Rule.CooldownSeconds != 900 {
		t.Fatalf("CooldownSeconds = %d, want default 900", validated.Rule.CooldownSeconds)
	}
}

func TestValidateRuleFormRejectsBadFields(t *testing.T) {
	tests := map[string]struct {
		input RuleFormInput
		field string
	}{
		"unknown metric": {
			input: RuleFormInput{Metric: "bogus", Threshold: "1"},
			field: "metric",
		},
		"missing threshold": {
			input: RuleFormInput{Metric: MetricCPU, Threshold: ""},
			field: "threshold",
		},
		"non-numeric threshold": {
			input: RuleFormInput{Metric: MetricCPU, Threshold: "abc"},
			field: "threshold",
		},
		"negative threshold": {
			input: RuleFormInput{Metric: MetricCPU, Threshold: "-1"},
			field: "threshold",
		},
		"zero consecutive hits": {
			input: RuleFormInput{Metric: MetricCPU, Threshold: "1", ConsecutiveHits: "0"},
			field: "consecutive_hits",
		},
		"bad comparator": {
			input: RuleFormInput{Metric: MetricCPU, Threshold: "1", Comparator: "ne"},
			field: "comparator",
		},
		"bad severity": {
			input: RuleFormInput{Metric: MetricCPU, Threshold: "1", Severity: "fatal"},
			field: "severity",
		},
		"bad server id": {
			input: RuleFormInput{Metric: MetricCPU, Threshold: "1", ServerID: "abc"},
			field: "server_id",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, errs := ValidateRuleForm(tc.input)
			if _, ok := errs[tc.field]; !ok {
				t.Fatalf("expected error on %q, got %#v", tc.field, errs)
			}
		})
	}
}

func TestValidateChannelForm(t *testing.T) {
	validated, errs := ValidateChannelForm(ChannelFormInput{
		Kind:    ChannelKindTelegram,
		Name:    "Ops",
		ChatID:  "-100123",
		Enabled: true,
	})
	if errs.HasAny() {
		t.Fatalf("unexpected validation errors: %#v", errs)
	}
	if validated.Channel.Name != "Ops" || validated.Channel.ChatID != "-100123" {
		t.Fatalf("unexpected channel: %#v", validated.Channel)
	}

	if _, errs := ValidateChannelForm(ChannelFormInput{Kind: ChannelKindTelegram, ChatID: "123"}); !errs.HasAny() {
		t.Fatal("expected error for empty name")
	} else if _, ok := errs["name"]; !ok {
		t.Fatalf("expected name error, got %#v", errs)
	}

	if _, errs := ValidateChannelForm(ChannelFormInput{Kind: ChannelKindTelegram, Name: "Ops"}); !errs.HasAny() {
		t.Fatal("expected error for empty chat id")
	} else if _, ok := errs["chat_id"]; !ok {
		t.Fatalf("expected chat_id error, got %#v", errs)
	}
}

func TestValidateSilenceForm(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

	validated, errs := ValidateSilenceForm(SilenceFormInput{
		ServerID:     "4",
		Metric:       MetricCPU,
		Reason:       "noisy host",
		ExpiresHours: "2",
	}, now)
	if errs.HasAny() {
		t.Fatalf("unexpected validation errors: %#v", errs)
	}
	if validated.Silence.ServerID != 4 {
		t.Fatalf("ServerID = %d, want 4", validated.Silence.ServerID)
	}
	if validated.Silence.ExpiresAt == nil {
		t.Fatal("expected ExpiresAt to be set")
	} else if want := now.Add(2 * time.Hour); !validated.Silence.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", validated.Silence.ExpiresAt, want)
	}

	// The "all" wildcard is a valid silence metric.
	if _, errs := ValidateSilenceForm(SilenceFormInput{ServerID: "1", Metric: MetricAll}, now); errs.HasAny() {
		t.Fatalf("expected 'all' to be valid, got %#v", errs)
	}

	// Missing server is rejected.
	if _, errs := ValidateSilenceForm(SilenceFormInput{ServerID: "", Metric: MetricCPU}, now); errs["server_id"] == "" {
		t.Fatal("expected server_id error")
	}

	// Unknown metric is rejected.
	if _, errs := ValidateSilenceForm(SilenceFormInput{ServerID: "1", Metric: "bogus"}, now); errs["metric"] == "" {
		t.Fatal("expected metric error")
	}

	// Blank expiry leaves the silence indefinite.
	indefinite, errs := ValidateSilenceForm(SilenceFormInput{ServerID: "1", Metric: MetricCPU}, now)
	if errs.HasAny() {
		t.Fatalf("unexpected validation errors: %#v", errs)
	}
	if indefinite.Silence.ExpiresAt != nil {
		t.Fatalf("ExpiresAt = %v, want nil", indefinite.Silence.ExpiresAt)
	}
}
