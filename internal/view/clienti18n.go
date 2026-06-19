package view

import (
	"encoding/json"
	"html/template"

	"github.com/Ho3einK84/Nodexia/internal/i18n"
)

// clientI18nKeys are the catalog keys the client-side JavaScript needs at
// runtime (confirm dialogs, toasts, aria-labels, status words, live counters).
// Only these keys are serialised into the page for the browser — the full
// catalog never reaches the client. Keep this list in sync with the literals
// the *.js files resolve via window.nxT / window.nxTn; the parity-style test in
// clienti18n_test.go fails if any key here is missing from a catalog.
var clientI18nKeys = []string{
	// Shared (app.js)
	"js.working",
	"js.loading",
	"js.failed",
	"js.confirm_irreversible",
	"js.flash.dismiss",
	"js.secret.reveal",
	"js.secret.hide",
	"js.copy.aria",
	"js.copy.copied",
	"js.copy.label",
	"js.copy.aria_label",
	"js.cred.load_error",
	"js.cred.show",
	"js.cred.hide",
	"js.cred.api_key",
	"js.cred.api_key_missing",
	"js.cred.ssl_cert",
	"js.stream.complete",
	"js.stream.refresh",
	"js.node_result.dismiss",
	"js.node_result.closing",
	"js.refresh.next_in",
	"js.back_to_top",

	// Bulk actions (bulk.js)
	"js.bulk.selected",
	"js.bulk.server_count",
	"js.bulk.confirm_delete",
	"js.bulk.confirm_reboot",
	"js.bulk.confirm_update",
	"js.bulk.confirm_node_restart",
	"js.bulk.confirm_node_update",
	"js.bulk.confirm_generic",

	// Stream result meta (app.js) — reuse the server-rendered exit label.
	"commands.exit",

	// Bulk live row/summary chips (app.js) — reuse the server-rendered labels.
	"bulk.status_ok",
	"bulk.status_failed",
	"bulk.status_skipped",
	"bulk.status_pending",
	"bulk.status_running",
	"bulk.count_ok",
	"bulk.count_failed",
	"bulk.count_skipped",
	"bulk.count_in_progress",

	// Analytics charts & forecast (analytics.js)
	"js.analytics.chart_footer",
	"js.analytics.forecast_unavailable",
	"js.analytics.today",
	"js.analytics.this_week",
	"js.analytics.this_month",
	"js.analytics.download_scope",
	"js.analytics.predicted_end",
	"js.analytics.period_elapsed",
	"js.analytics.trend_increasing",
	"js.analytics.trend_decreasing",
	"js.analytics.trend_stable",
	"js.analytics.confidence",
	"js.analytics.confidence_low",
	"js.analytics.confidence_medium",
	"js.analytics.confidence_high",
	"js.analytics.risk_spike",
	"js.analytics.risk_growth",
	"js.analytics.risk_exhaustion",
	"js.analytics.exhaustion_title",
	"js.analytics.exhaustion_over",
	"js.analytics.exhaustion_today",
	"js.analytics.exhaustion_in_days",
	"js.analytics.exhaustion_safe",
	"js.analytics.exhaustion_margin",

	// File browser (files.js)
	"js.files.request_failed",
	"js.files.upload_failed",
	"js.files.upload_network_error",
	"js.files.uploads_failed",
	"js.files.upload_done",
	"js.files.menu_download",
	"js.files.menu_rename",
	"js.files.menu_copy_path",
	"js.files.path_copied",
	"js.files.path_copy_failed",
	"js.files.clipboard_unavailable",
	"js.files.rename_prompt",
	"js.files.renamed",
	"js.files.confirm_delete_dir",
	"js.files.confirm_delete_file",
	"js.files.deleted",
	"js.files.mkdir_prompt",
	"js.files.created",
	"js.files.sort_desc",
	"js.files.sort_asc",
	"common.open",
	"common.delete",

	// Live metrics (livemetrics.js)
	"js.live.cores",
	"js.live.live",
	"js.live.reconnecting",
	"js.live.swap",
	"monitoring.connecting",

	// Interactive terminal (terminal.js)
	"js.terminal.connected",
	"js.terminal.disconnected",
	"js.terminal.status_error",
	"js.terminal.connection_error",
	"js.terminal.ws_failed",
	"js.terminal.closed_unexpectedly",
	"js.terminal.paste_prompt",
	"js.terminal.select",
	"js.terminal.done",
	"js.terminal.copy_all",
	"js.terminal.paste",
	"js.terminal.keys_label",
	"js.terminal.aria_left",
	"js.terminal.aria_up",
	"js.terminal.aria_down",
	"js.terminal.aria_right",
	"js.terminal.aria_select",
	"js.terminal.aria_copy",
	"js.terminal.aria_copy_all",
	"js.terminal.aria_font_smaller",
	"js.terminal.aria_font_larger",
}

// clientI18nJSON serialises the client-needed translations for loc into a JSON
// object embedded in the page as a non-executable <script type="application/json">
// island (CSP-safe — the browser never executes it; app.js parses it). The
// json encoder escapes HTML metacharacters (so "</script>" can never appear),
// and the result is marked template.JS so html/template emits it verbatim.
func clientI18nJSON(loc *i18n.Localizer) template.JS {
	if loc == nil {
		return template.JS("{}")
	}
	messages := loc.ClientMessages(clientI18nKeys)
	encoded, err := json.Marshal(messages)
	if err != nil {
		return template.JS("{}")
	}
	return template.JS(encoded)
}
