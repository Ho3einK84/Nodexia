package nodes

import (
	"strings"
	"testing"
	"time"
)

const pgDiscoveryFixture = `=DOCKER=
node	pasarguard/node:latest	Up 2 hours	0.0.0.0:443->443/tcp, 0.0.0.0:62050->62050/tcp
node2	pasarguard/node:v0.5.0	Exited (0) 3 hours ago
caddy	caddy:2	Up 5 hours
orphan	pasarguard/node:latest	Up 1 hour
=DOCKEREND=
=PGNODE=node=
=IMAGE=    image: pasarguard/node:latest=
=DATADIR=/var/lib/node=
=ENVSTART=
SERVICE_PORT = 62050
API_KEY = "11111111-2222-3333-4444-555555555555"
SERVICE_PROTOCOL = grpc
=ENVEND=
=PGNODEEND=
=PGNODE=node2=
=IMAGE=image: "pasarguard/node:v0.5.0"=
=ENVSTART=
SERVICE_PORT=5000
=ENVEND=
=PGNODEEND=
`

func TestPasarGuardParseDiscovery(t *testing.T) {
	collectedAt := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	snapshots := PasarGuardProvider{}.ParseDiscovery(pgDiscoveryFixture, collectedAt)

	if len(snapshots) != 3 {
		t.Fatalf("len(snapshots) = %d, want 3 (node, node2, orphan)", len(snapshots))
	}

	byName := map[string]Snapshot{}
	for _, snapshot := range snapshots {
		byName[snapshot.ServiceName] = snapshot
		if snapshot.NodeType != "pasarguard-node" {
			t.Fatalf("NodeType = %q, want pasarguard-node", snapshot.NodeType)
		}
	}

	node := byName["node"]
	if node.HealthStatus != "running" {
		t.Errorf("node HealthStatus = %q, want running", node.HealthStatus)
	}
	if node.ServicePort != "62050" {
		t.Errorf("node ServicePort = %q, want 62050", node.ServicePort)
	}
	if node.Protocol != "grpc" {
		t.Errorf("node Protocol = %q, want grpc", node.Protocol)
	}
	if node.Version != "latest" {
		t.Errorf("node Version = %q, want latest", node.Version)
	}
	if node.DataDir != "/var/lib/node" {
		t.Errorf("node DataDir = %q, want /var/lib/node", node.DataDir)
	}
	if node.Confidence != "high" {
		t.Errorf("node Confidence = %q, want high", node.Confidence)
	}
	if !containsString(node.XrayPorts, "443") {
		t.Errorf("node XrayPorts = %v, want to contain 443", node.XrayPorts)
	}
	if containsString(node.XrayPorts, "62050") {
		t.Errorf("node XrayPorts = %v, must not contain the service port", node.XrayPorts)
	}

	node2 := byName["node2"]
	if node2.HealthStatus != "stopped" {
		t.Errorf("node2 HealthStatus = %q, want stopped", node2.HealthStatus)
	}
	if node2.ServicePort != "5000" {
		t.Errorf("node2 ServicePort = %q, want 5000", node2.ServicePort)
	}
	if node2.Version != "v0.5.0" {
		t.Errorf("node2 Version = %q, want v0.5.0", node2.Version)
	}
	if node2.DataDir != "/var/lib/node2" {
		t.Errorf("node2 DataDir = %q, want /var/lib/node2 fallback", node2.DataDir)
	}
	// Protocol falls back to the PasarGuard default when .env omits it.
	if node2.Protocol != "grpc" {
		t.Errorf("node2 Protocol = %q, want grpc default", node2.Protocol)
	}

	orphan, ok := byName["orphan"]
	if !ok {
		t.Fatalf("expected a snapshot for the orphan container without /opt directory")
	}
	if orphan.HealthStatus != "running" {
		t.Errorf("orphan HealthStatus = %q, want running", orphan.HealthStatus)
	}
	if orphan.Confidence != "medium" {
		t.Errorf("orphan Confidence = %q, want medium (no .env evidence)", orphan.Confidence)
	}

	if _, found := byName["caddy"]; found {
		t.Fatalf("non-PasarGuard container must not produce a snapshot")
	}
}

func TestPasarGuardParseDiscoveryEmpty(t *testing.T) {
	if snapshots := (PasarGuardProvider{}).ParseDiscovery("", time.Now()); len(snapshots) != 0 {
		t.Fatalf("len(snapshots) = %d, want 0", len(snapshots))
	}
}

func TestPasarGuardActionCommand(t *testing.T) {
	provider := PasarGuardProvider{}

	command, timeout, err := provider.ActionCommand("node2", "restart")
	if err != nil {
		t.Fatalf("ActionCommand: %v", err)
	}
	if !strings.Contains(command, "--name node2 restart -n") {
		t.Errorf("restart command = %q, want pg-node restart with --name and -n", command)
	}
	if timeout <= 0 {
		t.Errorf("restart timeout = %v, want > 0", timeout)
	}

	command, _, err = provider.ActionCommand("node", "uninstall")
	if err != nil {
		t.Fatalf("ActionCommand uninstall: %v", err)
	}
	if !strings.Contains(command, "uninstall --yes") {
		t.Errorf("uninstall command = %q, want auto-confirmed uninstall", command)
	}

	if _, _, err := provider.ActionCommand("node; rm -rf /", "status"); err == nil {
		t.Fatalf("ActionCommand must reject shell metacharacters in node names")
	}
	if _, _, err := provider.ActionCommand("node", "format-disk"); err == nil {
		t.Fatalf("ActionCommand must reject unknown actions")
	}
}

func TestPasarGuardInstallCommand(t *testing.T) {
	command, err := PasarGuardProvider{}.InstallCommand("node3")
	if err != nil {
		t.Fatalf("InstallCommand: %v", err)
	}
	for _, want := range []string{pasarguardScriptURL, "install --name node3 --yes", "timeout"} {
		if !strings.Contains(command, want) {
			t.Errorf("install command missing %q:\n%s", want, command)
		}
	}

	if _, err := (PasarGuardProvider{}).InstallCommand("$(reboot)"); err == nil {
		t.Fatalf("InstallCommand must reject unsafe node names")
	}
}

func TestParseRegistrationInfo(t *testing.T) {
	output := `=ENVSTART=
SERVICE_PORT = 62050
API_KEY = "11111111-2222-3333-4444-555555555555"
SERVICE_PROTOCOL = grpc
=ENVEND=
=CERTSTART=
-----BEGIN CERTIFICATE-----
MIIBxTCCAWugAwIBAgIUO
-----END CERTIFICATE-----
=CERTEND=
`
	info, found := ParseRegistrationInfo("node", output)
	if !found {
		t.Fatalf("ParseRegistrationInfo found = false, want true")
	}
	if info.APIKey != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("APIKey = %q", info.APIKey)
	}
	if info.ServicePort != "62050" {
		t.Errorf("ServicePort = %q, want 62050", info.ServicePort)
	}
	if info.Protocol != "grpc" {
		t.Errorf("Protocol = %q, want grpc", info.Protocol)
	}
	if !strings.Contains(info.Certificate, "BEGIN CERTIFICATE") {
		t.Errorf("Certificate = %q, want PEM block", info.Certificate)
	}

	if _, found := ParseRegistrationInfo("node", "=ENVSTART=\n=ENVEND=\n"); found {
		t.Fatalf("ParseRegistrationInfo must report not-found without an API key")
	}
}
