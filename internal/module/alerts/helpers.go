package alerts

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, false
	}
	return id, true
}

func formatID(id int64) string {
	return strconv.FormatInt(id, 10)
}

func formatTimestamp(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format("2006-01-02 15:04:05 UTC")
}

// humanizeDurationSeconds renders a cooldown as a compact, readable duration.
func humanizeDurationSeconds(seconds int) string {
	if seconds <= 0 {
		return "no cooldown"
	}

	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60

	var parts []string
	if hours > 0 {
		parts = append(parts, strconv.Itoa(hours)+"h")
	}
	if minutes > 0 {
		parts = append(parts, strconv.Itoa(minutes)+"m")
	}
	if secs > 0 && hours == 0 {
		parts = append(parts, strconv.Itoa(secs)+"s")
	}
	if len(parts) == 0 {
		return "0s"
	}
	return strings.Join(parts, " ")
}

func flashKind(r *http.Request) string {
	switch r.URL.Query().Get("flash") {
	case "rule-created", "rule-updated", "rule-deleted",
		"channel-created", "channel-updated", "channel-deleted",
		"silenced", "silence-removed":
		return "success"
	default:
		return ""
	}
}

// flashMessage returns a translation key for the flash marker; renderOverview
// resolves it in the request's active language.
func flashMessage(r *http.Request) string {
	switch r.URL.Query().Get("flash") {
	case "rule-created":
		return "alerts.flash.rule_created"
	case "rule-updated":
		return "alerts.flash.rule_updated"
	case "rule-deleted":
		return "alerts.flash.rule_deleted"
	case "channel-created":
		return "alerts.flash.channel_created"
	case "channel-updated":
		return "alerts.flash.channel_updated"
	case "channel-deleted":
		return "alerts.flash.channel_deleted"
	case "silenced":
		return "alerts.flash.silenced"
	case "silence-removed":
		return "alerts.flash.silence_removed"
	default:
		return ""
	}
}

// redirectURL builds an /alerts redirect carrying a flash marker.
func redirectURL(flash string) string {
	return "/alerts?flash=" + flash
}

func serverLabel(names map[int64]string, id *int64) string {
	if id == nil {
		return "All servers"
	}
	if name, ok := names[*id]; ok {
		return fmt.Sprintf("%s (#%d)", name, *id)
	}
	return fmt.Sprintf("Server #%d", *id)
}
