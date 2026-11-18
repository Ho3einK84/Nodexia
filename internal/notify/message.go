package notify

import (
	"fmt"
	"strings"
	"text/template"
)

// Message state values, mirrored on AlertMessage.State so templates can vary
// firing vs resolved text.
const (
	StateFiring   = "firing"
	StateResolved = "resolved"
)

// AlertMessage is the data exposed to an alert message template. Values are
// pre-formatted strings (for example "93%" or "≥ 90%") so templates stay simple
// and need no numeric formatting. State is "firing" or "resolved"; FiredAt
// carries the fired time for firing messages and the resolved time for resolved
// ones.
type AlertMessage struct {
	Server    string
	Metric    string
	Value     string
	Threshold string
	Severity  string
	FiredAt   string
	State     string
}

// DefaultMessageTemplate is used when a channel has no custom template. It is
// plain text (no Telegram parse_mode) so message content never needs escaping.
const DefaultMessageTemplate = `{{ if eq .State "resolved" -}}
✅ RESOLVED · {{ .Server }}
{{ .Metric }} is back to normal (was {{ .Value }}, threshold {{ .Threshold }})
Resolved at {{ .FiredAt }}
{{- else -}}
{{ icon .Severity }} {{ upper .Severity }} alert · {{ .Server }}
{{ .Metric }} = {{ .Value }} (threshold {{ .Threshold }})
Fired at {{ .FiredAt }}
{{- end }}`

var messageFuncs = template.FuncMap{
	"upper": strings.ToUpper,
	"lower": strings.ToLower,
	"icon":  severityIcon,
}

func severityIcon(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return "🔴"
	case "warning":
		return "🟠"
	case "info":
		return "🔵"
	default:
		return "🔔"
	}
}

// RenderMessage renders an alert message. An empty override falls back to
// DefaultMessageTemplate. The override is parsed with the same helper functions
// (upper, lower, icon) so channel authors can reuse them.
func RenderMessage(override string, msg AlertMessage) (string, error) {
	text := strings.TrimSpace(override)
	if text == "" {
		text = DefaultMessageTemplate
	}

	tmpl, err := template.New("alert-message").Funcs(messageFuncs).Parse(text)
	if err != nil {
		return "", fmt.Errorf("notify: parse message template: %w", err)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, msg); err != nil {
		return "", fmt.Errorf("notify: render message template: %w", err)
	}

	return buf.String(), nil
}
