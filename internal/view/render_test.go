package view

import (
	"net/http/httptest"
	"testing"
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
		NodeInstallForm: NodeInstallFormView{
			Action:  "/servers/1/nodes/install",
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
