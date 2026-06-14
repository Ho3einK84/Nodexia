package nodes

import (
	"strings"
	"testing"
	"time"
)

// pgDiscoveryFixture mirrors what a real server produces. The install dirs and
// their containers have DIFFERENT names: /opt/pg-node (the default, no --name)
// runs container "node" (its compose service name), and /opt/node2 runs "node2".
// The probe resolves each container name via =CONTAINER= so the install links to
// its container instead of producing a ghost duplicate. "orphan" is a PasarGuard
// container with no /opt directory (a manual install) and is surfaced on its own.
const pgDiscoveryFixture = `=DOCKER=
node	pasarguard/node:latest	Up 2 hours	0.0.0.0:443->443/tcp, 0.0.0.0:62050->62050/tcp
node2	pasarguard/node:v0.5.0	Exited (0) 3 hours ago
caddy	caddy:2	Up 5 hours
orphan	pasarguard/node:latest	Up 1 hour
=DOCKEREND=
=PGNODE=pg-node=
=CONTAINER=node=
=IMAGE=    image: pasarguard/node:latest=
=DATADIR=/var/lib/pg-node=
=STATE=running=
=ENVSTART=
SERVICE_PORT = 62050
API_KEY = "11111111-2222-3333-4444-555555555555"
SERVICE_PROTOCOL = grpc
=ENVEND=
=PGNODEEND=
=PGNODE=node2=
=CONTAINER=node2=
=IMAGE=image: "pasarguard/node:v0.5.0"=
=STATE=exited=
=ENVSTART=
SERVICE_PORT=5000
=ENVEND=
=PGNODEEND=
`

func TestPasarGuardParseDiscovery(t *testing.T) {
	collectedAt := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	snapshots := PasarGuardProvider{}.ParseDiscovery(pgDiscoveryFixture, collectedAt)

	if len(snapshots) != 3 {
		t.Fatalf("len(snapshots) = %d, want 3 (pg-node, node2, orphan)", len(snapshots))
	}

	byName := map[string]Snapshot{}
	for _, snapshot := range snapshots {
		byName[snapshot.ServiceName] = snapshot
		if snapshot.NodeType != "pasarguard-node" {
			t.Fatalf("NodeType = %q, want pasarguard-node", snapshot.NodeType)
		}
	}

	// The default install: directory /opt/pg-node, container "node". It must be a
	// single entry named for its directory and linked to the "node" container
	// (health/version/ports come from that container) — never two entries.
	pgNode := byName["pg-node"]
	if pgNode.HealthStatus != "running" {
		t.Errorf("pg-node HealthStatus = %q, want running", pgNode.HealthStatus)
	}
	if pgNode.ServicePort != "62050" {
		t.Errorf("pg-node ServicePort = %q, want 62050", pgNode.ServicePort)
	}
	if pgNode.Protocol != "grpc" {
		t.Errorf("pg-node Protocol = %q, want grpc", pgNode.Protocol)
	}
	if pgNode.Version != "latest" {
		t.Errorf("pg-node Version = %q, want latest (from linked container)", pgNode.Version)
	}
	if pgNode.DataDir != "/var/lib/pg-node" {
		t.Errorf("pg-node DataDir = %q, want /var/lib/pg-node", pgNode.DataDir)
	}
	if pgNode.Confidence != "high" {
		t.Errorf("pg-node Confidence = %q, want high", pgNode.Confidence)
	}
	if !containsString(pgNode.XrayPorts, "443") {
		t.Errorf("pg-node XrayPorts = %v, want to contain 443", pgNode.XrayPorts)
	}
	if containsString(pgNode.XrayPorts, "62050") {
		t.Errorf("pg-node XrayPorts = %v, must not contain the service port", pgNode.XrayPorts)
	}
	if !containsSubstring(pgNode.Evidence, "Docker container: node") {
		t.Errorf("pg-node Evidence = %v, want it linked to container \"node\"", pgNode.Evidence)
	}

	// The bug: container "node" must NOT also appear as its own snapshot — it is
	// claimed by the /opt/pg-node install above.
	if _, dup := byName["node"]; dup {
		t.Fatalf("container \"node\" must not produce a separate snapshot; it belongs to /opt/pg-node")
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
	// No =DATADIR= was emitted (/var/lib/node2 does not exist), so DataDir must be
	// empty rather than a guessed path that is not on the host.
	if node2.DataDir != "" {
		t.Errorf("node2 DataDir = %q, want empty (no =DATADIR= probe marker)", node2.DataDir)
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

// containsSubstring reports whether any element of values contains sub.
func containsSubstring(values []string, sub string) bool {
	for _, v := range values {
		if strings.Contains(v, sub) {
			return true
		}
	}
	return false
}

// TestPasarGuardDiscoveryNoGhostDuplicate reproduces the exact reported server
// state: `docker ps` shows containers "node2" and "node", the default install is
// /opt/pg-node (whose container is "node") and a named install is /opt/node2.
// Before the fix the default install appeared twice — as "pg-node" (dir scan,
// inspect by "pg-node" found nothing) and as "node" (orphan fallback). It must
// now collapse to exactly one "pg-node" entry linked to the "node" container.
func TestPasarGuardDiscoveryNoGhostDuplicate(t *testing.T) {
	const fixture = `=DOCKER=
node2	pasarguard/node:latest	Up 1 hour	0.0.0.0:62051->62051/tcp
node	pasarguard/node:latest	Up 2 hours	0.0.0.0:62050->62050/tcp
=DOCKEREND=
=PGNODE=pg-node=
=CONTAINER=node=
=IMAGE=    image: pasarguard/node:latest=
=DATADIR=/var/lib/pg-node=
=STATE=running=
=ENVSTART=
SERVICE_PORT = 62050
=ENVEND=
=PGNODEEND=
=PGNODE=node2=
=CONTAINER=node2=
=IMAGE=    image: pasarguard/node:latest=
=STATE=running=
=ENVSTART=
SERVICE_PORT = 62051
=ENVEND=
=PGNODEEND=
`
	snapshots := PasarGuardProvider{}.ParseDiscovery(fixture, time.Now())
	if len(snapshots) != 2 {
		names := make([]string, len(snapshots))
		for i, s := range snapshots {
			names[i] = s.ServiceName
		}
		t.Fatalf("len(snapshots) = %d %v, want 2 (pg-node, node2) with no ghost duplicate", len(snapshots), names)
	}

	byName := map[string]Snapshot{}
	for _, s := range snapshots {
		byName[s.ServiceName] = s
	}
	if _, dup := byName["node"]; dup {
		t.Errorf("container \"node\" produced a separate snapshot; it belongs to the /opt/pg-node install")
	}
	pgNode, ok := byName["pg-node"]
	if !ok {
		t.Fatalf("missing pg-node snapshot; got %v", byName)
	}
	if pgNode.HealthStatus != "running" {
		t.Errorf("pg-node HealthStatus = %q, want running (from linked \"node\" container)", pgNode.HealthStatus)
	}
	if !containsString(pgNode.ActivePorts, "62050") {
		t.Errorf("pg-node ActivePorts = %v, want service port 62050", pgNode.ActivePorts)
	}
}

func TestPGHealth(t *testing.T) {
	cases := []struct {
		name            string
		state           string
		hasContainer    bool
		containerStatus string
		want            string
	}{
		{"inspect running", "running", false, "", "running"},
		{"inspect restarting", "restarting", false, "", "running"},
		{"inspect exited", "exited", true, "Up 1h", "stopped"}, // inspect wins over stale ps
		{"inspect paused", "paused", false, "", "stopped"},
		{"no inspect, ps up", "", true, "Up 2 hours", "running"},
		{"no inspect, ps exited", "", true, "Exited (0) ago", "stopped"},
		{"no signal at all", "", false, "", "unknown"},
	}
	for _, tc := range cases {
		if got := pgHealth(tc.state, tc.hasContainer, tc.containerStatus); got != tc.want {
			t.Errorf("%s: pgHealth(%q,%v,%q) = %q, want %q", tc.name, tc.state, tc.hasContainer, tc.containerStatus, got, tc.want)
		}
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
	// uninstall must NOT use --yes (the script's confirm is broken under --yes);
	// instead it pipes "y" to the uninstall + remove-data prompts.
	if strings.Contains(command, "--yes") {
		t.Errorf("uninstall command = %q, must not use --yes", command)
	}
	if !strings.Contains(command, `printf "y\ny\n" |`) || !strings.Contains(command, "--name node uninstall") {
		t.Errorf("uninstall command = %q, want piped y answers to uninstall", command)
	}

	if _, _, err := provider.ActionCommand("node; rm -rf /", "status"); err == nil {
		t.Fatalf("ActionCommand must reject shell metacharacters in node names")
	}
	if _, _, err := provider.ActionCommand("node", "format-disk"); err == nil {
		t.Fatalf("ActionCommand must reject unknown actions")
	}
}

func TestPasarGuardInstallCommand(t *testing.T) {
	cfg := InstallConfig{ServicePort: "62011", Protocol: "grpc"}
	command, err := PasarGuardProvider{}.InstallCommand("node3", cfg)
	if err != nil {
		t.Fatalf("InstallCommand: %v", err)
	}
	for _, want := range []string{pasarguardScriptURL, "install --name node3", "timeout", "62011"} {
		if !strings.Contains(command, want) {
			t.Errorf("install command missing %q:\n%s", want, command)
		}
	}
	if strings.Contains(command, "--yes") {
		t.Errorf("install command must not pass --yes (it locks port to 62050):\n%s", command)
	}

	if _, err := (PasarGuardProvider{}).InstallCommand("$(reboot)", cfg); err == nil {
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

func TestInstallConfigNormalize(t *testing.T) {
	// Defaults fill in when empty.
	cfg, err := InstallConfig{}.Normalize()
	if err != nil {
		t.Fatalf("Normalize empty: %v", err)
	}
	if cfg.ServicePort != "62050" || cfg.Protocol != "grpc" {
		t.Errorf("defaults = %+v, want port 62050 / grpc", cfg)
	}

	if _, err := (InstallConfig{ServicePort: "70000"}).Normalize(); err == nil {
		t.Errorf("Normalize must reject out-of-range service port")
	}
	if _, err := (InstallConfig{Protocol: "udp"}).Normalize(); err == nil {
		t.Errorf("Normalize must reject unknown protocol")
	}
	if _, err := (InstallConfig{APIKey: "not-a-uuid"}).Normalize(); err == nil {
		t.Errorf("Normalize must reject malformed API key")
	}
	if _, err := (InstallConfig{APIKey: "11111111-2222-3333-4444-555555555555"}).Normalize(); err != nil {
		t.Errorf("Normalize must accept a valid UUID: %v", err)
	}
}

func TestPasarGuardConfigureCommand(t *testing.T) {
	provider := PasarGuardProvider{}
	cfg := InstallConfig{ServicePort: "62055", APIPort: "62056", Protocol: "rest", APIKey: "11111111-2222-3333-4444-555555555555"}

	command, timeout, err := provider.ConfigureCommand("node2", cfg)
	if err != nil {
		t.Fatalf("ConfigureCommand: %v", err)
	}
	if timeout <= 0 {
		t.Errorf("timeout = %v, want > 0", timeout)
	}
	for _, want := range []string{
		"/opt/node2/.env",
		"SERVICE_PORT= 62055",
		`SERVICE_PROTOCOL= \"rest\"`,
		"API_PORT= 62056",
		"API_KEY= 11111111-2222-3333-4444-555555555555",
		"--name node2 restart -n",
	} {
		if !strings.Contains(command, want) {
			t.Errorf("configure command missing %q:\n%s", want, command)
		}
	}

	// Optional fields omitted when empty.
	command, _, err = provider.ConfigureCommand("node", InstallConfig{ServicePort: "62050", Protocol: "grpc"})
	if err != nil {
		t.Fatalf("ConfigureCommand minimal: %v", err)
	}
	if strings.Contains(command, "API_PORT=") || strings.Contains(command, "API_KEY=") {
		t.Errorf("empty optional fields must not be written:\n%s", command)
	}

	if _, _, err := provider.ConfigureCommand("node; rm -rf /", cfg); err == nil {
		t.Errorf("ConfigureCommand must reject unsafe node names")
	}
}

func TestNormalizeInstallInput(t *testing.T) {
	provider := PasarGuardProvider{}

	cfg, errs := provider.normalizeInstallInput(installFormInput{
		NodeName: "node2", ServicePort: "62055", Protocol: "rest",
	})
	if errs.HasAny() {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if cfg.ServicePort != "62055" || cfg.Protocol != "rest" {
		t.Errorf("cfg = %+v", cfg)
	}

	_, errs = provider.normalizeInstallInput(installFormInput{
		ServicePort: "abc", APIPort: "99999", Protocol: "udp", APIKey: "x",
	})
	for _, field := range []string{"service_port", "api_port", "protocol", "api_key"} {
		if _, ok := errs[field]; !ok {
			t.Errorf("expected validation error for %q, got %v", field, errs)
		}
	}
}
