package view

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/config"
)

// rawKeyPattern matches a leaked translation key (an area prefix followed by a
// dotted segment) so the smoke test fails if any {{ t }} call references a key
// missing from the catalog — the fallback renders the raw key, which this
// catches in both languages.
var rawKeyPattern = regexp.MustCompile(`\b(servers|common|nav|home|diagnostics|login|bulk|terminal|files|system|error|module|pagination|shell|lang|drawer|commands|monitoring|analytics|alerts|nodes)\.[a-z_]+(\.[a-z_]+)*\b`)

// clientI18nIslandPattern matches the non-executable JSON island the layout
// emits for the browser-side JS (window.nxT). It legitimately contains catalog
// keys as JSON property names, so it is stripped before scanning for leaks.
var clientI18nIslandPattern = regexp.MustCompile(`(?s)<script type="application/json" id="nodexia-i18n">.*?</script>`)

// translatedPages enumerates the content templates that phase 2 has localized.
// Each renders end-to-end in every supported locale; any raw-key leak or
// template execution error fails the build. Extend this list as more areas are
// translated.
func translatedPages() map[string]func(p *PageData) {
	return map[string]func(p *PageData){
		"content-home": func(p *PageData) {},
		"content-servers-list": func(p *PageData) {
			p.Servers = []ServerSummary{{ID: 1, Name: "edge", Host: "10.0.0.1", Port: 22, AuthMode: "password", CredentialStrategy: "stored", Status: "up", JustNow: true, CreatedAt: "2026-01-01"}}
		},
		"content-server-form":        func(p *PageData) { p.ServerForm = ServerFormView{Errors: map[string]string{}} },
		"content-login":              func(p *PageData) {},
		"content-diagnostics":        func(p *PageData) {},
		"content-error":              func(p *PageData) { p.ErrorTitle = "x"; p.ErrorMessage = "y" },
		"content-module-placeholder": func(p *PageData) {},
		"content-bulk-result": func(p *PageData) {
			p.BulkActionResult = BulkActionResultView{Available: true, ActionLabel: "reboot", Results: []BulkServerResultView{{Name: "edge", Status: "ok"}}, Total: 1}
		},
		"content-terminal": func(p *PageData) { p.TerminalForm = TerminalFormView{Errors: map[string]string{}} },
		"content-files": func(p *PageData) {
			p.FileForm = FileFormView{Errors: map[string]string{}}
			p.FileListing = FileListingView{Available: true}
		},
		"content-system":             func(p *PageData) { p.SystemForm = SystemFormView{Errors: map[string]string{}} },
		"content-commands":           func(p *PageData) { p.CommandForm = CommandFormView{Errors: map[string]string{}} },
		"content-monitoring":         func(p *PageData) { p.MonitoringForm = MonitoringFormView{Errors: map[string]string{}} },
		"content-analytics":          func(p *PageData) {},
		"content-analytics-global":   func(p *PageData) { p.GlobalAnalytics = GlobalAnalyticsView{ServerCount: 0} },
		"content-analytics-limits":   func(p *PageData) { p.TrafficLimits = TrafficLimitsView{UnitOptions: []string{"GiB", "TiB"}} },
		"content-alerts-overview":    func(p *PageData) { p.AlertsOverview = AlertsOverviewView{} },
		"content-alert-rule-form":    func(p *PageData) { p.AlertRuleForm = AlertRuleFormView{Errors: map[string]string{}} },
		"content-alert-channel-form": func(p *PageData) { p.AlertChannelForm = AlertChannelFormView{Errors: map[string]string{}} },
		"content-nodes": func(p *PageData) {
			p.NodeForm = NodeFormView{Errors: map[string]string{}}
			p.NodeInstallForm = NodeInstallFormView{Enabled: true, Errors: map[string]string{}}
			p.NodeRebeccaInstallForm = NodeRebeccaInstallFormView{
				Enabled:  true,
				Channels: []NodeInstallChannelView{{Key: "dev", Enabled: true}, {Key: "stable", Enabled: false}},
				Errors:   map[string]string{},
			}
		},
		"content-node-install": func(p *PageData) { p.NodeInstall = NodeInstallView{Available: true} },
	}
}

func TestTranslatedPagesNoRawKeyLeak(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	for _, lang := range []string{"en", "fa"} {
		for tmpl, setup := range translatedPages() {
			t.Run(lang+"/"+tmpl, func(t *testing.T) {
				page := NewPageData(config.Config{App: config.AppConfig{Name: "Nodexia"}}, requestWithLang(t, lang))
				page.ContentTemplate = tmpl
				setup(&page)

				rec := httptest.NewRecorder()
				if err := renderer.Render(rec, http.StatusOK, page); err != nil {
					t.Fatalf("render %s (%s): %v", tmpl, lang, err)
				}
				// The client-i18n JSON island contains catalog keys by design;
				// strip it so only genuine leaks in rendered copy are flagged.
				body := clientI18nIslandPattern.ReplaceAllString(rec.Body.String(), "")
				if m := rawKeyPattern.FindString(body); m != "" {
					t.Errorf("%s (%s): leaked raw translation key %q", tmpl, lang, m)
				}
			})
		}
	}
}
