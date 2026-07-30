package view

import (
	"io/fs"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	assets "github.com/Ho3einK84/Nodexia"
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

	if !strings.Contains(script, `var isOpen = stored ? stored !== 'closed' : !defaultClosed;`) {
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
	script := string(raw)

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
