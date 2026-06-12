package nodes

import (
	"strings"
	"testing"
	"time"
)

const rebeccaDiscoveryFixture = `=REBECCA=
=ENVSTART=
SERVICE_PORT=62033
XRAY_API_PORT=62034
SERVICE_PROTOCOL=rest
REBECCA_DATA_DIR=/var/lib/rebecca-node
=ENVEND=
=RELEASESTART=
{"install_mode":"binary","image":"rebecca-node (binary)","tag":"v0.2.1","arch":"linux-amd64"}
=RELEASEEND=
=MODE=binary=
=SERVICE=active=
=CONTAINER==
=REBECCAEND=
`

func TestRebeccaParseDiscovery(t *testing.T) {
	collectedAt := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	snapshots := RebeccaProvider{}.ParseDiscovery(rebeccaDiscoveryFixture, collectedAt)

	if len(snapshots) != 1 {
		t.Fatalf("len(snapshots) = %d, want 1", len(snapshots))
	}
	snapshot := snapshots[0]

	if snapshot.NodeType != "rebecca-node" {
		t.Errorf("NodeType = %q", snapshot.NodeType)
	}
	if snapshot.ServiceName != "rebecca-node" {
		t.Errorf("ServiceName = %q", snapshot.ServiceName)
	}
	if snapshot.Version != "v0.2.1" {
		t.Errorf("Version = %q, want v0.2.1", snapshot.Version)
	}
	if snapshot.InstallMode != "binary" {
		t.Errorf("InstallMode = %q, want binary", snapshot.InstallMode)
	}
	if snapshot.HealthStatus != "running" {
		t.Errorf("HealthStatus = %q, want running", snapshot.HealthStatus)
	}
	if snapshot.ServicePort != "62033" {
		t.Errorf("ServicePort = %q, want 62033", snapshot.ServicePort)
	}
	if snapshot.APIPort != "62034" {
		t.Errorf("APIPort = %q, want 62034", snapshot.APIPort)
	}
	if snapshot.Protocol != "rest" {
		t.Errorf("Protocol = %q, want rest", snapshot.Protocol)
	}
	if snapshot.DataDir != "/var/lib/rebecca-node" {
		t.Errorf("DataDir = %q", snapshot.DataDir)
	}
	if snapshot.Confidence != "high" {
		t.Errorf("Confidence = %q, want high", snapshot.Confidence)
	}
}

func TestRebeccaParseDiscoveryNotInstalled(t *testing.T) {
	if snapshots := (RebeccaProvider{}).ParseDiscovery("", time.Now()); len(snapshots) != 0 {
		t.Fatalf("len(snapshots) = %d, want 0", len(snapshots))
	}
}

func TestRebeccaParseDiscoveryUnreadableConfig(t *testing.T) {
	// Marker present (directory exists) but files were unreadable: defaults apply.
	output := "=REBECCA=\n=ENVSTART=\n=ENVEND=\n=RELEASESTART=\n=RELEASEEND=\n=MODE==\n=SERVICE=inactive=\n=REBECCAEND=\n"
	snapshots := RebeccaProvider{}.ParseDiscovery(output, time.Now())
	if len(snapshots) != 1 {
		t.Fatalf("len(snapshots) = %d, want 1", len(snapshots))
	}
	snapshot := snapshots[0]
	if snapshot.ServicePort != rebeccaDefaultServicePort {
		t.Errorf("ServicePort = %q, want default %s", snapshot.ServicePort, rebeccaDefaultServicePort)
	}
	if snapshot.APIPort != rebeccaDefaultAPIPort {
		t.Errorf("APIPort = %q, want default %s", snapshot.APIPort, rebeccaDefaultAPIPort)
	}
	if snapshot.HealthStatus != "stopped" {
		t.Errorf("HealthStatus = %q, want stopped", snapshot.HealthStatus)
	}
	if snapshot.Confidence != "medium" {
		t.Errorf("Confidence = %q, want medium", snapshot.Confidence)
	}
}

func TestRebeccaHealthDockerMode(t *testing.T) {
	if got := rebeccaHealth("docker", "", "Up 3 hours"); got != "running" {
		t.Errorf(`rebeccaHealth(docker, "", "Up 3 hours") = %q, want running`, got)
	}
	if got := rebeccaHealth("docker", "", "Exited (1) 2 hours ago"); got != "stopped" {
		t.Errorf("rebeccaHealth docker exited = %q, want stopped", got)
	}
	if got := rebeccaHealth("binary", "active", ""); got != "running" {
		t.Errorf("rebeccaHealth binary active = %q, want running", got)
	}
	if got := rebeccaHealth("binary", "", ""); got != "unknown" {
		t.Errorf("rebeccaHealth binary no-signal = %q, want unknown", got)
	}
}

func TestRebeccaActionCommand(t *testing.T) {
	provider := RebeccaProvider{}

	command, _, err := provider.ActionCommand("rebecca-node", "logs")
	if err != nil {
		t.Fatalf("ActionCommand logs: %v", err)
	}
	if !strings.Contains(command, "rebecca-node logs --no-follow") {
		t.Errorf("logs command = %q, want non-following logs", command)
	}

	command, _, err = provider.ActionCommand("rebecca-node", "uninstall")
	if err != nil {
		t.Fatalf("ActionCommand uninstall: %v", err)
	}
	if !strings.Contains(command, "yes | $SUDO rebecca-node uninstall") {
		t.Errorf("uninstall command = %q, want auto-confirmed uninstall", command)
	}

	if _, _, err := provider.ActionCommand("rebecca-node", "install"); err == nil {
		t.Fatalf("Rebecca must not expose an install action")
	}
}
