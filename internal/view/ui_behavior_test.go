package view

import (
	"io/fs"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	assets "github.com/Ho3einK84/Nodexia"
	"github.com/Ho3einK84/Nodexia/internal/config"
)

func renderUIBehaviorFixture(t *testing.T, data PageData) string {
	t.Helper()

	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	recorder := httptest.NewRecorder()
	if err := renderer.Render(recorder, 200, data); err != nil {
		t.Fatalf("Render %s: %v", data.ContentTemplate, err)
	}
	return recorder.Body.String()
}

func TestNodeCardKeepsSummaryOutsideCollapsedBody(t *testing.T) {
	body := renderUIBehaviorFixture(t, PageData{
		AppName:         "Nodexia",
		ContentTemplate: "content-nodes",
		NodeTarget:      NodeTargetView{ID: 1, Name: "edge-1"},
		NodeSnapshots: []NodeSnapshotView{
			{
				Name:           "node2",
				NodeType:       "pasarguard-node",
				TypeLabel:      "PasarGuard",
				InstallMode:    "docker",
				Version:        "0.5.3",
				HealthStatus:   "running",
				ActivePorts:    []string{"62050"},
				XrayPorts:      []string{"443"},
				ServicePort:    "62050",
				APIPort:        "62051",
				Protocol:       "grpc",
				DataDir:        "/var/lib/node2",
				Confidence:     "high",
				Dependencies:   []string{"docker:available"},
				Evidence:       []string{"Config: /opt/node2/.env"},
				CollectedAt:    "2026-07-30 12:00:00 UTC",
				ActionsEnabled: true,
				Actions: []NodeActionView{
					{Key: "restart", Label: "Restart", Icon: "rotate-cw"},
				},
			},
		},
	})

	cardStart := strings.Index(body, `<article class="node-card node-card--pasarguard-node collapsible">`)
	if cardStart < 0 {
		t.Fatal("rendered nodes page is missing the collapsible node card")
	}
	cardEndOffset := strings.Index(body[cardStart:], "</article>")
	if cardEndOffset < 0 {
		t.Fatal("rendered node card has no closing article tag")
	}
	card := body[cardStart : cardStart+cardEndOffset]

	toggle := `data-collapse-key="node_body_pasarguard-node_node2"`
	toggleIndex := strings.Index(card, toggle)
	headerEnd := strings.Index(card, "</header>")
	statusIndex := strings.Index(card, `class="node-status-row"`)
	contentIndex := strings.Index(card, `class="collapsible__content node-card__body"`)
	menuIndex := strings.Index(card, `data-action-menu`)
	if toggleIndex < 0 {
		t.Fatalf("node body toggle is missing its stable key:\n%s", card)
	}
	if headerEnd < 0 || toggleIndex > headerEnd {
		t.Error("node body toggle must stay in the always-visible card header")
	} else if !strings.Contains(card[toggleIndex:headerEnd], `data-collapse-default="closed"`) {
		t.Error("node body toggle must default to closed")
	}
	if statusIndex < 0 || contentIndex < 0 || statusIndex > contentIndex {
		t.Error("node status row must stay outside the collapsed card body")
	}
	confIndex := strings.Index(card, `class="node-confidence `)
	if confIndex < 0 || confIndex < headerEnd || confIndex > contentIndex {
		t.Error("confidence meter should live inside the compact status row rather than crowding the card title header")
	}
	if !strings.Contains(body, `<details class="card card--collapsible node-install-launch">`) {
		t.Error("install node launcher card must default closed as a collapsible details panel")
	}
	if menuIndex < 0 || menuIndex > contentIndex {
		t.Error("node action menu must stay outside the collapsed card body")
	}

	for _, marker := range []string{
		`class="node-facts"`,
		`class="node-section node-credentials"`,
		"Active ports",
		"Xray proxy ports",
		"Dependencies",
		`class="node-evidence collapsible"`,
		`class="node-card__footer"`,
	} {
		index := strings.Index(card, marker)
		if index < contentIndex {
			t.Errorf("%q must render inside the collapsed card body", marker)
		}
	}
}

func TestEveryTemplateCollapsibleDefaultsClosed(t *testing.T) {
	togglePattern := regexp.MustCompile(`(?s)<button\b[^>]*class="[^"]*\bcollapsible__toggle\b[^"]*"[^>]*>`)
	keyPattern := regexp.MustCompile(`data-collapse-key="([^"]+)"`)
	seenKeys := map[string]bool{}

	err := fs.WalkDir(assets.Templates(), "web/templates", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".gohtml") {
			return nil
		}
		raw, err := fs.ReadFile(assets.Templates(), path)
		if err != nil {
			return err
		}
		for _, toggle := range togglePattern.FindAllString(string(raw), -1) {
			if !strings.Contains(toggle, `data-collapse-default="closed"`) {
				t.Errorf("%s has a collapsible toggle without a closed default: %s", path, toggle)
			}
			if match := keyPattern.FindStringSubmatch(toggle); len(match) == 2 {
				seenKeys[match[1]] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded templates: %v", err)
	}

	for _, key := range []string{
		"traffic_collect",
		"mon_stdout",
		"mon_stderr",
		"node_body_{{ .NodeType }}_{{ .Name }}",
		"evidence_{{ .NodeType }}_{{ .Name }}",
		"probe_{{ .Label }}",
		"node_scope",
		"sys_stdout",
		"sys_stderr",
	} {
		if !seenKeys[key] {
			t.Errorf("template collapsible audit did not find data-collapse-key %q", key)
		}
	}
}

func TestCollapsibleInitializationPreservesStoredPreferenceAndRescan(t *testing.T) {
	staticFS, err := assets.Static()
	if err != nil {
		t.Fatalf("open embedded static assets: %v", err)
	}
	raw, err := fs.ReadFile(staticFS, "app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	script := string(raw)

	uiScript := script
	if uiRaw, err := fs.ReadFile(staticFS, "modules/ui.js"); err == nil {
		uiScript = string(uiRaw)
	}

	if !strings.Contains(uiScript, `var isOpen = stored ? stored !== 'closed' : !defaultClosed;`) {
		t.Error("initCollapsibles no longer gives a stored preference precedence over the template default")
	}
	if strings.Count(script, "initCollapsibles(root);") < 1 {
		t.Error("NodexiaApp.rescan no longer initializes collapsibles in injected content")
	}
}

func TestWorkspaceLinkGestureRoutingSource(t *testing.T) {
	staticFS, err := assets.Static()
	if err != nil {
		t.Fatalf("open embedded static assets: %v", err)
	}
	raw, err := fs.ReadFile(staticFS, "tab-manager.js")
	if err != nil {
		t.Fatalf("read tab-manager.js: %v", err)
	}
	script := strings.ReplaceAll(string(raw), "\r\n", "\n")

	start := strings.Index(script, "function initLinkInterception()")
	if start < 0 {
		t.Fatal("could not find initLinkInterception")
	}
	end := strings.Index(script[start:], "/* ── CSRF token refresh")
	if end < 0 {
		t.Fatal("could not isolate initLinkInterception")
	}
	interception := script[start : start+end]

	modifierBranch := `if (window.innerWidth >= MOBILE_BREAKPOINT && (e.metaKey || e.ctrlKey)) {
        open(url.pathname + url.search, { background: true });
        return;
      }`
	plainNavigation := `var tab = tabsById[activeTabId];
      if (tab) navigateInPane(tab, url.pathname + url.search, {}, true);`
	middleClick := `if (e.button !== 1) return;`
	if !strings.Contains(interception, modifierBranch) {
		t.Error("desktop Ctrl/Cmd+click must create a background workspace tab")
	}
	if !strings.Contains(interception, plainNavigation) {
		t.Error("plain same-origin clicks must navigate the active pane")
	}
	if !strings.Contains(interception, middleClick) ||
		strings.Count(interception, `open(url.pathname + url.search, { background: true });`) < 2 {
		t.Error("middle-click must create a background workspace tab")
	}

	linkSheetStart := strings.Index(script, "function showLinkSheet(")
	if linkSheetStart < 0 {
		t.Fatal("could not find the mobile link action sheet")
	}
	linkSheetEnd := strings.Index(script[linkSheetStart:], "/* ── Long-press")
	if linkSheetEnd < 0 {
		t.Fatal("could not isolate the mobile link action sheet")
	}
	linkSheet := script[linkSheetStart : linkSheetStart+linkSheetEnd]
	if !strings.Contains(linkSheet, `open(url, { background: true });`) {
		t.Error("mobile long-press action sheet must retain its explicit new-tab action")
	}
}

func TestTerminalToolbarIsExcludedFromWorkspaceSwipe(t *testing.T) {
	staticFS, err := assets.Static()
	if err != nil {
		t.Fatalf("open embedded static assets: %v", err)
	}

	tabManagerRaw, err := fs.ReadFile(staticFS, "tab-manager.js")
	if err != nil {
		t.Fatalf("read tab-manager.js: %v", err)
	}
	if !strings.Contains(string(tabManagerRaw), `e.target.closest('.terminal-toolbar')`) {
		t.Error("mobile workspace swipe must ignore touches that originate in the terminal toolbar")
	}
	if !strings.Contains(string(tabManagerRaw), `.terminal-ac-strip`) {
		t.Error("mobile workspace swipe must ignore touches that originate in the terminal autocomplete strip")
	}

	terminalCSSRaw, err := fs.ReadFile(staticFS, "terminal.css")
	if err != nil {
		t.Fatalf("read terminal.css: %v", err)
	}
	toolbarRule := regexp.MustCompile(`(?s)\.terminal-toolbar\s*\{[^}]*touch-action:\s*pan-x;`)
	if !toolbarRule.Match(terminalCSSRaw) {
		t.Error("terminal toolbar must reserve horizontal touch gestures for its own scrolling")
	}
	acStripRule := regexp.MustCompile(`(?s)\.terminal-ac-strip\s*\{[^}]*touch-action:\s*pan-x;`)
	if !acStripRule.Match(terminalCSSRaw) {
		t.Error("terminal autocomplete strip must reserve horizontal touch gestures for its own scrolling")
	}
}

func TestTerminalClientResumesExistingSessionAfterVisibilityLoss(t *testing.T) {
	staticFS, err := assets.Static()
	if err != nil {
		t.Fatalf("open embedded static assets: %v", err)
	}
	raw, err := fs.ReadFile(staticFS, "terminal.js")
	if err != nil {
		t.Fatalf("read terminal.js: %v", err)
	}
	script := string(raw)

	for _, contract := range []string{
		`'?ticket=' + encodeURIComponent(ticket)`,
		`addGlobalListener(document, 'visibilitychange'`,
		`socket.close(4000, 'resume probe timeout')`,
		`if (everConnected) setStatus('reconnecting'`,
		`removeGlobalListeners();`,
	} {
		if !strings.Contains(script, contract) {
			t.Errorf("terminal resume client is missing %q", contract)
		}
	}
}

func TestLiveMetricsSocketIsScopedAndDisposedWithItsPane(t *testing.T) {
	staticFS, err := assets.Static()
	if err != nil {
		t.Fatalf("open embedded static assets: %v", err)
	}
	raw, err := fs.ReadFile(staticFS, "livemetrics.js")
	if err != nil {
		t.Fatalf("read livemetrics.js: %v", err)
	}
	script := string(raw)

	for _, contract := range []string{
		`document.currentScript && document.currentScript.closest('.tab-pane')`,
		`if (reconnectTimer) {`,
		`cleanupRoot.__nxCleanups.push(stop);`,
	} {
		if !strings.Contains(script, contract) {
			t.Errorf("live metrics pane cleanup is missing %q", contract)
		}
	}
}

// The fleet header is rendered straight from PageData.NodeFleet; a regression
// that leaves the struct at its zero value must fail loudly rather than
// silently render "ACTIVE: 0" next to running node cards.
func TestNodeFleetHeaderRendersAggregates(t *testing.T) {
	body := renderUIBehaviorFixture(t, PageData{
		AppName:         "Nodexia",
		ContentTemplate: "content-nodes",
		NodeTarget:      NodeTargetView{ID: 1, Name: "edge-1"},
		NodeFleet: NodeFleetView{
			Total:       2,
			Running:     1,
			Stopped:     1,
			Ports:       2,
			PortList:    []string{"62011", "62050"},
			HealthPct:   97,
			HealthKnown: true,
		},
		NodeSnapshots: []NodeSnapshotView{
			{Name: "node2", NodeType: "pasarguard-node", TypeLabel: "PasarGuard", HealthStatus: "running", ServicePort: "62011"},
		},
	})

	fleetStart := strings.Index(body, `class="nodes-fleet"`)
	if fleetStart < 0 {
		t.Fatal("rendered nodes page is missing the fleet header")
	}
	// Take the whole header block, not just the first stat cell.
	header := body[fleetStart:]
	if end := strings.Index(header, `</section>`); end > 0 {
		header = header[:end]
	}

	if !strings.Contains(header, `data-total="2"`) {
		t.Errorf("fleet header lost its total: %s", header)
	}
	for _, want := range []string{`>1</span>`, `>2</span>`, `97%`, `:62011`, `:62050`} {
		if !strings.Contains(header, want) {
			t.Errorf("fleet header missing %q:\n%s", want, header)
		}
	}
}

func TestServerCardKeepsActionsInHeaderWithoutDuplicates(t *testing.T) {
	body := renderUIBehaviorFixture(t, PageData{
		AppName:         "Nodexia",
		ContentTemplate: "content-servers-list",
		ServerCount:     1,
		ServersUpCount:  1,
		TotalNodeCount:  2,
		Servers: []ServerSummary{{
			ID: 7, Name: "edge-nl", Host: "203.0.113.10", Port: 22, Username: "root",
			AuthMode: "password", CredentialStrategy: "stored", Status: "up", JustNow: true,
			CreatedAt: "2026-01-01", FlagEmoji: "\U0001F1F3\U0001F1F1", CountryCode: "NL", CountryName: "Netherlands",
			HasTelemetry: true, CPUPercent: 18, RAMPercent: 46, DiskPercent: 32,
			CPULevel: "ok", RAMLevel: "ok", DiskLevel: "ok",
			NodeCount: 2, RunningNodeCount: 2,
		}},
	})

	cardStart := strings.Index(body, `<article class="server-card server-card--collapsible`)
	if cardStart < 0 {
		t.Fatal("rendered servers page is missing the collapsible server card")
	}
	cardEnd := strings.Index(body[cardStart:], "</article>")
	if cardEnd < 0 {
		t.Fatal("rendered server card has no closing article tag")
	}
	card := body[cardStart : cardStart+cardEnd]

	bodyIndex := strings.Index(card, `class="server-card__collapsible-body"`)
	menuIndex := strings.Index(card, `data-action-menu`)
	headerEndIndex := strings.Index(card, `class="server-card__header-end"`)
	if bodyIndex < 0 || headerEndIndex < 0 {
		t.Fatalf("server card is missing its header-end or collapsible body:\n%s", card)
	}
	if menuIndex < headerEndIndex || menuIndex > bodyIndex {
		t.Error("server action menu must live in the always-visible header, not the collapsed body")
	}
	for _, path := range []string{
		"/servers/7/monitoring",
		"/servers/7/nodes",
		"/servers/7/terminal",
		"/servers/7/files",
		"/servers/7/analytics",
	} {
		if n := strings.Count(card, `href="`+path+`"`); n != 1 {
			t.Errorf("%s must appear exactly once per card (header quick action), got %d", path, n)
		}
	}
	if !strings.Contains(card, `<code class="server-card__country" title="Netherlands">NL</code>`) {
		t.Error("server card must show the ISO country code tag")
	}
	if strings.Contains(card, "\U0001F1F3\U0001F1F1") {
		t.Error("server card must not render the flag emoji")
	}
	if strings.Contains(card, "server-card--password") {
		t.Error("auth mode must not drive a card-level visual modifier")
	}
}

func TestDesktopSidebarAndMobileMenuContract(t *testing.T) {
	for _, lang := range []string{"en", "fa"} {
		body := renderHome(t, lang)
		for _, want := range []string{
			`id="shell-sidebar"`,
			`data-sidebar-toggle`,
			`aria-controls="shell-sidebar"`,
			`class="shell-topbar__divider"`,
			`class="shell-sidebar__body"`,
			`class="shell-sidebar__action"`,
			`class="shell-meta__env"`,
			`class="lang-switcher__short"`,
			`id="mobile-drawer"`,
			`class="bottom-nav"`,
			`data-drawer-open`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s rendered shell missing %q", lang, want)
			}
		}
	}
}

func TestPageHeadersPlaceDescriptionInInfoPopover(t *testing.T) {
	cases := []struct {
		name string
		data PageData
	}{
		{
			name: "home",
			data: PageData{
				AppName:         "Nodexia",
				ContentTemplate: "content-home",
				PageTitle:       "Operations overview",
				PageDescription: "Resource health, scheduled collection, and runtime status across your managed Rebecca and PasarGuard servers.",
			},
		},
		{
			name: "servers",
			data: PageData{
				AppName:         "Nodexia",
				ContentTemplate: "content-servers-list",
				PageTitle:       "Server registry",
				PageDescription: "Your managed Rebecca and PasarGuard servers. Register a target to start collecting monitoring and node data.",
			},
		},
		{
			name: "diagnostics",
			data: PageData{
				AppName:         "Nodexia",
				ContentTemplate: "content-diagnostics",
				PageTitle:       "Diagnostics",
				PageDescription: "Runtime health, background jobs, and support signals without reading application logs manually.",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := renderUIBehaviorFixture(t, tc.data)
			for _, unwanted := range []string{`<p class="page-description">`, `<p class="servers-hero__desc">`} {
				if strings.Contains(body, unwanted) {
					t.Errorf("%s header still renders inline paragraph %s", tc.name, unwanted)
				}
			}
			for _, want := range []string{
				`data-page-info`,
				`data-page-info-trigger`,
				`data-page-info-popover`,
				`<span class="page-info__text">` + tc.data.PageDescription + `</span>`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("%s header missing %q", tc.name, want)
				}
			}
		})
	}
}

func TestAnalyticsPagesRenderColorAndMotionHooks(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	for _, lang := range []string{"en", "fa"} {
		t.Run(lang+"/global", func(t *testing.T) {
			page := NewPageData(config.Config{App: config.AppConfig{Name: "Nodexia"}}, requestWithLang(t, lang))
			page.ContentTemplate = "content-analytics-global"
			page.PageTitle = page.T("analytics.global_title")
			page.GlobalAnalytics = GlobalAnalyticsView{
				ServerCount:        4,
				HasGlobalLimit:     true,
				GlobalLimitHuman:   "10 TB",
				TagLimitCount:      3,
				HasFleetTraffic:    true,
				FleetDownloadHuman: "15.32 TB",
				FleetUploadHuman:   "15.10 TB",
				FleetTotalHuman:    "30.42 TB",
				FleetRXPct:         50,
				FleetTXPct:         50,
				FleetMonthLabel:    "2026-11",
				AvgCPU:             "30.8%",
				AvgRAM:             "51.1%",
				AvgDisk:            "31.0%",
				AvgCPULevel:        "ok",
				AvgRAMLevel:        "ok",
				AvgDiskLevel:       "ok",
				TopMetrics: []TopServerMetricView{
					{Rank: 1, ServerID: 1, ServerName: "Germany", CountryName: "Germany", FlagEmoji: "\U0001F1E9\U0001F1EA", CPU: "62.4%", RAM: "85.2%", Disk: "28.0%", CPUPct: 62.4, RAMPct: 85.2, DiskPct: 28.0, CPULevel: "warn", RAMLevel: "crit", DiskLevel: "ok"},
				},
				TopTraffic: []TopServerTrafficView{
					{Rank: 1, ServerID: 4, ServerName: "Netherlands", CountryName: "Netherlands", FlagEmoji: "\U0001F1F3\U0001F1F1", Download: "5.56 TB", Upload: "5.50 TB", MonthBytes: "11.06 TB", MonthLabel: "2026-11", SharePct: 36, BarPct: 100, RXBarPct: 50, TXBarPct: 50},
				},
			}

			rec := httptest.NewRecorder()
			if err := renderer.Render(rec, 200, page); err != nil {
				t.Fatalf("render global (%s): %v", lang, err)
			}
			body := rec.Body.String()
			for _, want := range []string{
				`an-kpi--sky`, `an-kpi--indigo`, `an-kpi--ok`, `an-kpi--violet`,
				`an-avg-pills`, `an-meter__fill--warn`, `an-meter__fill--crit`, `an-meter__fill--ok`,
				`an-traffic-bar__rx`, `an-traffic-bar__tx`, `an-share-pill`, `an-split-summary`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("global (%s) missing %q", lang, want)
				}
			}
			stripped := headPattern.ReplaceAllString(clientI18nIslandPattern.ReplaceAllString(body, ""), "")
			if m := rawKeyPattern.FindString(stripped); m != "" {
				t.Errorf("global (%s) leaked raw translation key %q", lang, m)
			}
		})

		t.Run(lang+"/server", func(t *testing.T) {
			page := NewPageData(config.Config{App: config.AppConfig{Name: "Nodexia"}}, requestWithLang(t, lang))
			page.ContentTemplate = "content-analytics"
			page.PageTitle = page.T("analytics.page_title", "server", "nl-ams-02")
			page.AnalyticsTarget = AnalyticsTargetView{ID: 4, Name: "nl-ams-02"}
			page.AnalyticsTrafficMonth = AnalyticsTrafficSummaryView{
				HasData: true, MonthLabel: "2026-11",
				Download: "5.56 TB", Upload: "5.50 TB", Total: "11.06 TB",
				DownloadPct: 50, UploadPct: 50,
			}
			page.AnalyticsLimit = AnalyticsLimitView{
				Action: "/servers/4/analytics/limit", HasLimit: true,
				LimitHuman: "7.00 TB", ValueInput: "7", UnitInput: "TB",
				UnitOptions: []string{"GB", "TB"}, KindInput: "rx", KindLabel: "Download (RX)",
			}

			rec := httptest.NewRecorder()
			if err := renderer.Render(rec, 200, page); err != nil {
				t.Fatalf("render server (%s): %v", lang, err)
			}
			body := rec.Body.String()
			for _, want := range []string{
				`chart-card--traffic`, `chart-card--cpu`, `chart-card--ram`, `chart-card--disk`, `chart-card--load`,
				`id="rangeTabIndicator"`, `id="live-cpu"`, `id="live-traffic"`,
				`traffic-summary__pct--dl`, `traffic-summary__pct--ul`, `traffic-summary__split-rx`, `traffic-summary__split-tx`,
				`an-shimmer`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("server (%s) missing %q", lang, want)
				}
			}
			stripped := headPattern.ReplaceAllString(clientI18nIslandPattern.ReplaceAllString(body, ""), "")
			if m := rawKeyPattern.FindString(stripped); m != "" {
				t.Errorf("server (%s) leaked raw translation key %q", lang, m)
			}
		})

		t.Run(lang+"/limits", func(t *testing.T) {
			page := NewPageData(config.Config{App: config.AppConfig{Name: "Nodexia"}}, requestWithLang(t, lang))
			page.ContentTemplate = "content-analytics-limits"
			page.PageTitle = page.T("analytics.limits.title")
			page.TrafficLimits = TrafficLimitsView{
				GlobalAction: "/analytics/limits", TagAction: "/analytics/limits/tags", TagDeleteAction: "/analytics/limits/tags/delete",
				HasGlobal: true, GlobalHuman: "10 TB", GlobalValue: "10", GlobalUnit: "TB", UnitOptions: []string{"GB", "TB"},
				Tags: []TrafficLimitTagView{{Tag: "hetzner", LimitHuman: "5 TB"}},
			}

			rec := httptest.NewRecorder()
			if err := renderer.Render(rec, 200, page); err != nil {
				t.Fatalf("render limits (%s): %v", lang, err)
			}
			body := rec.Body.String()
			for _, want := range []string{`precedence-step--1`, `precedence-step--2`, `precedence-step--3`, `an-tag-chip`} {
				if !strings.Contains(body, want) {
					t.Errorf("limits (%s) missing %q", lang, want)
				}
			}
			stripped := headPattern.ReplaceAllString(clientI18nIslandPattern.ReplaceAllString(body, ""), "")
			if m := rawKeyPattern.FindString(stripped); m != "" {
				t.Errorf("limits (%s) leaked raw translation key %q", lang, m)
			}
		})
	}
}

func TestMobileNavAnimationAndCSSSmoothnessContract(t *testing.T) {
	staticFS, err := assets.Static()
	if err != nil {
		t.Fatalf("open embedded static assets: %v", err)
	}

	appRaw, err := fs.ReadFile(staticFS, "app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	if !strings.Contains(string(appRaw), "initBottomNav();") {
		t.Error("app.js boot must initialize mobile bottom-nav sliding animations via initBottomNav()")
	}

	shellJSRaw, err := fs.ReadFile(staticFS, "modules/shell.js")
	if err != nil {
		t.Fatalf("read modules/shell.js: %v", err)
	}
	for _, want := range []string{"function initBottomNav()", "bottom-nav__slider", "is-bouncing"} {
		if !strings.Contains(string(shellJSRaw), want) {
			t.Errorf("modules/shell.js missing mobile nav animation hook %q", want)
		}
	}

	shellCSSRaw, err := fs.ReadFile(staticFS, "shell.css")
	if err != nil {
		t.Fatalf("read shell.css: %v", err)
	}
	for _, want := range []string{".bottom-nav__slider", "@keyframes bn-icon-pop", "@keyframes bn-dock-in", ".drawer.is-open .drawer__link:nth-child(1)"} {
		if !strings.Contains(string(shellCSSRaw), want) {
			t.Errorf("shell.css missing mobile nav animation rule %q", want)
		}
	}

	cssComments := regexp.MustCompile(`(?s)/\*.*?\*/`)
	forbiddenCSS := regexp.MustCompile(`backdrop-filter\s*:|transition\s*:\s*all\b`)
	for _, file := range []string{"analytics.css", "shell.css", "tabs.css"} {
		raw, err := fs.ReadFile(staticFS, file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		cleaned := cssComments.ReplaceAllString(string(raw), "")
		if match := forbiddenCSS.FindString(cleaned); match != "" {
			t.Errorf("%s violates 60fps CSS guardrail with %q", file, match)
		}
	}
}

// The monthly cap is measured against download (RX) by default, so the quota
// pill must sit in the Download cell and never under Total.
func TestAnalyticsGlobalQuotaPillFollowsLimitKind(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	cases := []struct{ kind, cell string }{
		{"rx", "an-cell-traffic--dl"},
		{"tx", "an-cell-traffic--ul"},
		{"total", "an-cell-traffic--total"},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			page := NewPageData(config.Config{App: config.AppConfig{Name: "Nodexia"}}, requestWithLang(t, "en"))
			page.ContentTemplate = "content-analytics-global"
			page.PageTitle = page.T("analytics.global_title")
			page.GlobalAnalytics = GlobalAnalyticsView{
				ServerCount: 1,
				TopTraffic: []TopServerTrafficView{{
					Rank: 1, ServerID: 1, ServerName: "Germany", Download: "4.72 TB", Upload: "4.67 TB", MonthBytes: "9.39 TB",
					HasQuota: true, QuotaKind: tc.kind, QuotaPct: 24, QuotaLevel: "ok", QuotaLimitHuman: "20.00 TB",
				}},
			}
			rec := httptest.NewRecorder()
			if err := renderer.Render(rec, 200, page); err != nil {
				t.Fatalf("render: %v", err)
			}
			body := rec.Body.String()
			if n := strings.Count(body, "an-quota-pill an-quota-pill--ok"); n != 1 {
				t.Fatalf("expected exactly one quota pill, got %d", n)
			}
			start := strings.Index(body, tc.cell)
			end := strings.Index(body, "an-cell-action")
			pill := strings.Index(body, "an-quota-pill an-quota-pill--ok")
			if start < 0 || pill < start || pill > end {
				t.Fatalf("quota pill not inside %s cell", tc.cell)
			}
			for _, other := range []string{"an-cell-traffic--dl", "an-cell-traffic--ul", "an-cell-traffic--total"} {
				if other == tc.cell {
					continue
				}
				o := strings.Index(body, other)
				if o >= 0 && o > start && o < pill {
					t.Fatalf("quota pill rendered after %s cell, expected in %s", other, tc.cell)
				}
			}
		})
	}
}
