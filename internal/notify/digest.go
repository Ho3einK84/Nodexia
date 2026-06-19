package notify

import (
	"fmt"
	"strings"
	"text/template"
)

// DigestServer is one server's row in the periodic status digest. Like
// AlertMessage, every field is a pre-formatted string so the template stays free
// of numeric/locale formatting.
type DigestServer struct {
	Name          string
	MonthDownload string // month-to-date download, e.g. "120.50 GiB"
	MonthTotal    string // month-to-date download+upload total
	LimitState    string // human summary of the forecast / limit state
	ActiveAlerts  int    // count of currently-firing alert events for this server
}

// DigestMessage is the data exposed to the digest template. It is assembled from
// the same analytics summaries + forecast the analytics overview uses, plus the
// currently-active alert events.
type DigestMessage struct {
	GeneratedAt  string
	ServerCount  int
	ActiveAlerts int
	Servers      []DigestServer
}

// DefaultDigestTemplate renders the digest as plain text (no Telegram parse_mode)
// so content never needs escaping — matching DefaultMessageTemplate. The digest
// is English, consistent with alert notifications: the notify layer has no
// request-scoped locale, and Telegram messages are template-driven English today.
const DefaultDigestTemplate = `📊 {{ .GeneratedAt }} · Nodexia status digest
{{ .ServerCount }} server(s) · {{ .ActiveAlerts }} active alert(s)
{{ range .Servers }}
• {{ .Name }}
  MTD download {{ .MonthDownload }} (total {{ .MonthTotal }})
  {{ .LimitState }}{{ if .ActiveAlerts }}
  🔔 {{ .ActiveAlerts }} active alert(s){{ end }}
{{- else }}
No servers registered yet — nothing to report.
{{- end }}`

// RenderDigest renders a status digest. An empty override falls back to
// DefaultDigestTemplate. It reuses the same helper functions as RenderMessage.
func RenderDigest(override string, msg DigestMessage) (string, error) {
	text := strings.TrimSpace(override)
	if text == "" {
		text = DefaultDigestTemplate
	}

	tmpl, err := template.New("digest-message").Funcs(messageFuncs).Parse(text)
	if err != nil {
		return "", fmt.Errorf("notify: parse digest template: %w", err)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, msg); err != nil {
		return "", fmt.Errorf("notify: render digest template: %w", err)
	}

	return buf.String(), nil
}
