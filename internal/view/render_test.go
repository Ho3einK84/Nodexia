package view

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/config"
)

// renderSmoke executes a content template through the full layout with the
// given page data, failing the test on any template error.
func renderSmoke(t *testing.T, data PageData) {
	t.Helper()

	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	recorder := httptest.NewRecorder()
	if err := renderer.Render(recorder, 200, data); err != nil {
		t.Fatalf("Render %s: %v", data.ContentTemplate, err)
	}
	if recorder.Body.Len() == 0 {
		t.Fatalf("Render %s produced no output", data.ContentTemplate)
	}
}

func TestRenderNodesPage(t *testing.T) {
	data := PageData{
		AppName:         "Nodexia",
		ContentTemplate: "content-nodes",
		NodeTarget:      NodeTargetView{ID: 1, Name: "edge-1", Host: "192.0.2.10", Port: 22, Username: "root", AuthMode: "password"},
		NodeForm: NodeFormView{
			Action:                     "/servers/1/nodes",
			RefreshURL:                 "/servers/1/nodes",
			StoredCredentialsAvailable: true,
			Errors:                     map[string]string{},
		},
		NodeDeployForm: NodeDeployFormView{
			Action:      "/servers/1/nodes/install",
			NodeType:    "pasarguard-node",
			ServicePort: "62050",
			Protocol:    "grpc",
			Channel:     "stable",
			Channels:    []NodeInstallChannelView{{Key: "stable", Label: "nodes.channel_stable", Enabled: true}},
			Engines: []NodeDeployEngineView{
				{Key: "pasarguard-node", Label: "PasarGuard", Description: "Containerized node", Icon: "container", Recommended: true, Kind: "docker"},
				{Key: "rebecca-node", Label: "Rebecca", Description: "Native binary node", Icon: "package", Kind: "binary"},
			},
			Enabled: true,
			Errors:  map[string]string{},
		},
		NodeStream: CommandStreamView{
			Available:     true,
			IsRunning:     true,
			Command:       "PasarGuard node2 — restart",
			Stdout:        "restarting...",
			RefreshURL:    "/servers/1/nodes?stream=abc",
			RefreshMillis: 2000,
		},
		NodeSnapshots: []NodeSnapshotView{
			{
				Name:         "node2",
				NodeType:     "pasarguard-node",
				TypeLabel:    "PasarGuard",
				InstallMode:  "docker",
				Version:      "latest",
				HealthStatus: "running",
				ActivePorts:  []string{"62050"},
				XrayPorts:    []string{"443"},
				ServicePort:  "62050",
				APIPort:      "-",
				Protocol:     "grpc",
				DataDir:      "/var/lib/node2",
				Confidence:   "high",
				Dependencies: []string{"docker:available"},
				Evidence:     []string{"Config: /opt/node2/.env"},
				CollectedAt:  "2026-06-01 12:00:00 UTC",
				Actions: []NodeActionView{
					{Key: "restart", Label: "Restart", Icon: "rotate-cw"},
					{Key: "uninstall", Label: "Uninstall", Icon: "trash-2", Danger: true},
				},
				ActionsEnabled: true,
			},
		},
		NodeCollection: NodeCollectionResultView{
			Available:  true,
			ProbeCount: 2,
			Probes: []NodeProbeView{
				{Label: "pasarguard-node", Command: "sh -c ...", Stdout: "=DOCKER=\n=DOCKEREND="},
			},
		},
	}
	renderSmoke(t, data)
}

func TestRenderNodeInstallPage(t *testing.T) {
	data := PageData{
		AppName:         "Nodexia",
		ContentTemplate: "content-node-install",
		NodeTarget:      NodeTargetView{ID: 1, Name: "edge-1", Host: "192.0.2.10", Port: 22, Username: "root"},
		NodeInstall: NodeInstallView{
			Available:     true,
			JobID:         "abc",
			NodeName:      "node2",
			Status:        "completed",
			StartedAt:     "2026-06-01 12:00:00 UTC",
			FinishedAt:    "2026-06-01 12:03:00 UTC",
			Duration:      "3m0s",
			Output:        "installed",
			RefreshURL:    "/servers/1/nodes/install/abc",
			RefreshMillis: 2000,
			NodesURL:      "/servers/1/nodes",
			Info: NodeRegistrationView{
				Available:   true,
				NodeName:    "node2",
				NodeIP:      "192.0.2.10",
				ServicePort: "62050",
				Protocol:    "grpc",
				APIKey:      "11111111-2222-3333-4444-555555555555",
				Certificate: "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----",
			},
		},
	}
	renderSmoke(t, data)
}

func TestRenderPartialSkipsLayoutAndEmitsMetadata(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/servers", nil)
	req.Header.Set("X-Nodexia-Partial", "1")
	page := NewPageData(config.Config{App: config.AppConfig{Name: "Nodexia"}}, req)
	page.Title = "Servers"
	page.ContentTemplate = "content-servers-list"
	page.CSRFToken = "tok-123"
	page.PageStyles = []string{"/static/servers.css"}
	page.PageScripts = []string{"/static/bulk.js"}

	rec := httptest.NewRecorder()
	if err := renderer.Render(rec, http.StatusOK, page); err != nil {
		t.Fatalf("Render partial: %v", err)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<!doctype html>") || strings.Contains(body, `id="nodexia-i18n"`) || strings.Contains(body, `id="shell-sidebar"`) {
		t.Fatalf("partial response must not include full layout shell or i18n island")
	}
	for _, want := range []string{
		`<div id="tab-content" data-partial="1"`,
		`data-page-title="Servers"`,
		`data-csrf="tok-123"`,
		`/static/servers.css?v=`,
		`/static/bulk.js?v=`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("partial response missing %q", want)
		}
	}
	if got := rec.Header().Get("X-CSRF-Token"); got != "tok-123" {
		t.Errorf("X-CSRF-Token header = %q, want tok-123", got)
	}
	if vary := rec.Header().Get("Vary"); !strings.Contains(vary, "X-Nodexia-Partial") {
		t.Errorf("Vary header = %q, want X-Nodexia-Partial", vary)
	}
}
