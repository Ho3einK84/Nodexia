package nodes

import (
	"fmt"
	"strings"
	"time"
)

// PasarGuardProvider drives PasarGuard nodes (https://github.com/PasarGuard/node).
//
// PasarGuard nodes are Docker based and any number of instances can coexist on
// one server.  Each instance <name> owns /opt/<name> (docker-compose.yml +
// .env) and /var/lib/<name> (certs, xray core).  Instances are discovered by
// scanning those directories and the Docker container list — names are never
// hardcoded.  Management goes through the official `pg-node` CLI; installs go
// through the official install script.
type PasarGuardProvider struct{}

const (
	pasarguardType        = "pasarguard-node"
	pasarguardScriptURL   = "https://github.com/PasarGuard/scripts/raw/main/pg-node.sh"
	pasarguardDefaultPort = "62050"
	// pasarguardDefaultProtocol matches the install script default (gRPC).
	pasarguardDefaultProtocol = "grpc"
)

func (PasarGuardProvider) Type() string        { return pasarguardType }
func (PasarGuardProvider) DisplayName() string { return "PasarGuard" }

func (PasarGuardProvider) SupportsInstall() bool { return true }

// DiscoveryCommand enumerates every PasarGuard instance:
//   - a "=DOCKER=" section lists all containers (name, image, status, ports);
//   - one "=PGNODE=<name>=" block per /opt/<name> whose compose file
//     references PasarGuard, including its compose image line and .env.
func (PasarGuardProvider) DiscoveryCommand() string {
	return `sh -c '` +
		`if command -v docker >/dev/null 2>&1; then ` +
		`printf "=DOCKER=\n"; ` +
		`docker ps -a --format "{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}" 2>/dev/null || true; ` +
		`printf "=DOCKEREND=\n"; ` +
		`fi; ` +
		`for dir in /opt/*/; do ` +
		`[ -f "$dir/docker-compose.yml" ] || continue; ` +
		`grep -Eqi "pasarguard|pg-node" "$dir/docker-compose.yml" 2>/dev/null || continue; ` +
		`name="${dir%/}"; name="${name##*/}"; ` +
		`printf "=PGNODE=%s=\n" "$name"; ` +
		`printf "=IMAGE=%s=\n" "$(grep -i "image:" "$dir/docker-compose.yml" 2>/dev/null | head -n 1)"; ` +
		`[ -d "/var/lib/$name" ] && printf "=DATADIR=/var/lib/%s=\n" "$name"; ` +
		`printf "=ENVSTART=\n"; cat "$dir/.env" 2>/dev/null || true; printf "\n=ENVEND=\n"; ` +
		`printf "=PGNODEEND=\n"; ` +
		`done; true'`
}

// pgInstance is the parsed evidence for one PasarGuard install directory.
type pgInstance struct {
	Name        string
	ComposeLine string
	DataDir     string
	EnvLines    []string
}

// dockerEntry is one row of the `docker ps -a` section.
type dockerEntry struct {
	Name   string
	Image  string
	Status string
	Ports  string
}

func (p PasarGuardProvider) ParseDiscovery(output string, collectedAt time.Time) []Snapshot {
	lines := strings.Split(output, "\n")
	containers, dockerSeen := parseDockerSection(lines)
	instances := parsePGInstances(lines)

	snapshots := make([]Snapshot, 0, len(instances))
	seen := map[string]struct{}{}
	for _, inst := range instances {
		seen[strings.ToLower(inst.Name)] = struct{}{}
		snapshots = append(snapshots, p.buildSnapshot(inst, containers, dockerSeen, collectedAt))
	}

	// Containers running a PasarGuard image without a matching /opt directory
	// (manual installs) are still surfaced, with config defaults.
	for _, c := range containers {
		if !strings.Contains(strings.ToLower(c.Image), "pasarguard") {
			continue
		}
		if _, ok := seen[strings.ToLower(c.Name)]; ok {
			continue
		}
		seen[strings.ToLower(c.Name)] = struct{}{}
		snapshots = append(snapshots, p.buildSnapshot(pgInstance{Name: c.Name}, containers, dockerSeen, collectedAt))
	}

	return snapshots
}

func parseDockerSection(lines []string) (map[string]dockerEntry, bool) {
	section, seen := markerSection(lines, "=DOCKER=", "=DOCKEREND=")
	entries := map[string]dockerEntry{}
	for _, line := range section {
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		entry := dockerEntry{
			Name:   strings.TrimSpace(parts[0]),
			Image:  strings.TrimSpace(parts[1]),
			Status: strings.TrimSpace(parts[2]),
		}
		if len(parts) > 3 {
			entry.Ports = strings.TrimSpace(parts[3])
		}
		if entry.Name != "" {
			entries[strings.ToLower(entry.Name)] = entry
		}
	}
	return entries, seen
}

func parsePGInstances(lines []string) []pgInstance {
	var instances []pgInstance
	var current *pgInstance
	inEnv := false

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "=PGNODE=") && strings.HasSuffix(line, "="):
			name := strings.TrimSuffix(strings.TrimPrefix(line, "=PGNODE="), "=")
			current = &pgInstance{Name: strings.TrimSpace(name)}
			inEnv = false
		case line == "=PGNODEEND=":
			if current != nil && current.Name != "" {
				instances = append(instances, *current)
			}
			current = nil
			inEnv = false
		case current == nil:
		case strings.HasPrefix(line, "=IMAGE=") && strings.HasSuffix(line, "="):
			current.ComposeLine = strings.TrimSuffix(strings.TrimPrefix(line, "=IMAGE="), "=")
		case strings.HasPrefix(line, "=DATADIR=") && strings.HasSuffix(line, "="):
			current.DataDir = strings.TrimSuffix(strings.TrimPrefix(line, "=DATADIR="), "=")
		case line == "=ENVSTART=":
			inEnv = true
		case line == "=ENVEND=":
			inEnv = false
		case inEnv:
			current.EnvLines = append(current.EnvLines, raw)
		}
	}
	return instances
}

func (PasarGuardProvider) buildSnapshot(inst pgInstance, containers map[string]dockerEntry, dockerSeen bool, collectedAt time.Time) Snapshot {
	env := parseEnvFile(inst.EnvLines)
	container, hasContainer := containers[strings.ToLower(inst.Name)]

	servicePort := parsePortFromEnv(env, "SERVICE_PORT")
	if servicePort == "" {
		servicePort = pasarguardDefaultPort
	}
	apiPort := parsePortFromEnv(env, "API_PORT")
	protocol := cleanEnvValue(env["SERVICE_PROTOCOL"])
	if protocol == "" {
		protocol = pasarguardDefaultProtocol
	}

	dataDir := inst.DataDir
	if dataDir == "" {
		dataDir = "/var/lib/" + inst.Name
	}

	version := ""
	if hasContainer {
		version = extractImageTag(container.Image)
	}
	if version == "" {
		version = extractImageTag(composeImage(inst.ComposeLine))
	}

	health := "unknown"
	switch {
	case hasContainer && strings.HasPrefix(strings.ToLower(container.Status), "up"):
		health = "running"
	case hasContainer:
		health = "stopped"
	case dockerSeen:
		// Docker answered but no container exists for this instance yet.
		health = "stopped"
	}

	evidence := []string{
		fmt.Sprintf("Install directory: /opt/%s (docker-compose.yml)", inst.Name),
	}
	if len(env) > 0 {
		evidence = append(evidence, fmt.Sprintf("Config: /opt/%s/.env (service port %s, protocol %s)", inst.Name, servicePort, protocol))
	}
	if hasContainer {
		evidence = append(evidence, fmt.Sprintf("Docker container: %s (image: %s, status: %s)", container.Name, container.Image, container.Status))
	}
	if inst.DataDir != "" {
		evidence = append(evidence, "Data directory: "+inst.DataDir)
	}

	dependencies := []string{"docker:missing"}
	if dockerSeen {
		dependencies = []string{"docker:available"}
	}

	activePorts := uniqueStrings([]string{servicePort, apiPort})
	xrayPorts := containerXrayPorts(container.Ports, activePorts)

	confidence := "medium"
	if len(env) > 0 {
		confidence = "high"
	}

	return normalizeSnapshot(Snapshot{
		NodeType:     pasarguardType,
		ServiceName:  inst.Name,
		InstallMode:  "docker",
		Version:      version,
		HealthStatus: health,
		ActivePorts:  activePorts,
		XrayPorts:    xrayPorts,
		ServicePort:  servicePort,
		APIPort:      apiPort,
		Protocol:     protocol,
		DataDir:      dataDir,
		Confidence:   confidence,
		Dependencies: dependencies,
		Evidence:     evidence,
		CollectedAt:  collectedAt,
	})
}

// composeImage extracts the image reference from a compose "image:" line.
func composeImage(line string) string {
	line = strings.TrimSpace(line)
	if idx := strings.Index(strings.ToLower(line), "image:"); idx != -1 {
		line = line[idx+len("image:"):]
	}
	return strings.Trim(strings.TrimSpace(line), `"'`)
}

// containerXrayPorts extracts published host ports from a `docker ps` Ports
// column, excluding the node management ports.
func containerXrayPorts(portsColumn string, mgmt []string) []string {
	var out []string
	for _, segment := range strings.Split(portsColumn, ",") {
		port := parseContainerPort(segment)
		if port == "" || containsString(mgmt, port) {
			continue
		}
		out = append(out, port)
	}
	return uniqueStrings(out)
}

func parseContainerPort(segment string) string {
	segment = strings.TrimSpace(segment)
	if segment == "" {
		return ""
	}
	if strings.Contains(segment, "->") {
		segment = strings.SplitN(segment, "->", 2)[0]
	}
	if idx := strings.LastIndex(segment, ":"); idx != -1 {
		segment = segment[idx+1:]
	}
	if idx := strings.Index(segment, "/"); idx != -1 {
		segment = segment[:idx]
	}
	segment = strings.Trim(segment, "[] ")
	return parsePortFromEnv(map[string]string{"p": segment}, "p")
}

// ── Management actions ────────────────────────────────────────────────────────

func (PasarGuardProvider) Actions() []Action {
	return []Action{
		{Key: "start", Label: "Start", Icon: "play", Timeout: 3 * time.Minute},
		{Key: "stop", Label: "Stop", Icon: "square", Timeout: 3 * time.Minute},
		{Key: "restart", Label: "Restart", Icon: "rotate-cw", Timeout: 5 * time.Minute},
		{Key: "status", Label: "Status", Icon: "activity", Timeout: 2 * time.Minute},
		{Key: "logs", Label: "Logs", Icon: "scroll-text", Timeout: 2 * time.Minute},
		{Key: "update", Label: "Update", Icon: "arrow-up-circle", Timeout: 20 * time.Minute},
		{Key: "core-update", Label: "Core update", Icon: "cpu", Timeout: 10 * time.Minute},
		{Key: "renew-cert", Label: "Renew cert", Icon: "shield-check", Timeout: 5 * time.Minute},
		{Key: "uninstall", Label: "Uninstall", Icon: "trash-2", Danger: true, Timeout: 10 * time.Minute},
	}
}

// pasarguardOps maps action keys to pg-node CLI operations.  Flags keep every
// operation non-interactive: -n stops up/restart/logs from tailing forever,
// --yes auto-confirms destructive prompts.
var pasarguardOps = map[string]string{
	"start":       "up -n",
	"stop":        "down",
	"restart":     "restart -n",
	"status":      "status",
	"logs":        "logs --no-follow",
	"update":      "update --yes",
	"core-update": "core-update --yes",
	"renew-cert":  "renew-cert --yes",
	"uninstall":   "uninstall --yes",
}

// ActionCommand builds `pg-node --name <node> <op>`.  The install script
// registers the CLI under the instance name (/usr/local/bin/<name>) when a
// custom --name was used, so the command falls back to that before giving up
// with exit 86.
func (p PasarGuardProvider) ActionCommand(nodeName, actionKey string) (string, time.Duration, error) {
	if err := ValidateNodeName(nodeName); err != nil {
		return "", 0, err
	}
	action, ok := actionByKey(p.Actions(), actionKey)
	if !ok {
		return "", 0, fmt.Errorf("nodes: pasarguard: unsupported action %q", actionKey)
	}
	op := pasarguardOps[action.Key]

	command := `sh -c '` + sudoPreamble +
		`if command -v pg-node >/dev/null 2>&1; then PG_CLI="pg-node"; ` +
		`elif command -v ` + nodeName + ` >/dev/null 2>&1; then PG_CLI="` + nodeName + `"; ` +
		`else echo "pg-node CLI not found on this server" >&2; exit 86; fi; ` +
		`$SUDO "$PG_CLI" --name ` + nodeName + ` ` + op + ` </dev/null'`
	return command, action.Timeout, nil
}

// ── Installation ──────────────────────────────────────────────────────────────

// pasarguardInstallScriptTimeout bounds the install script on the remote side:
// after a successful install the script tails container logs with no opt-out,
// so GNU timeout cuts it off and the runner verifies the result by reading the
// installed configuration (RegistrationInfoCommand).
const pasarguardInstallScriptTimeout = "600"

// InstallCommand downloads and runs the official PasarGuard install script
// non-interactively for a new instance named nodeName.
func (PasarGuardProvider) InstallCommand(nodeName string) (string, error) {
	if err := ValidateNodeName(nodeName); err != nil {
		return "", err
	}
	command := `sh -c '` + sudoPreamble +
		`SCRIPT="$(mktemp /tmp/nodexia-pg-node.XXXXXX)" || exit 1; ` +
		`if command -v curl >/dev/null 2>&1; then curl -fsSL ` + pasarguardScriptURL + ` -o "$SCRIPT" || { echo "download failed" >&2; rm -f "$SCRIPT"; exit 85; }; ` +
		`elif command -v wget >/dev/null 2>&1; then wget -qO "$SCRIPT" ` + pasarguardScriptURL + ` || { echo "download failed" >&2; rm -f "$SCRIPT"; exit 85; }; ` +
		`else echo "curl or wget is required to install" >&2; rm -f "$SCRIPT"; exit 85; fi; ` +
		`TMO=""; if command -v timeout >/dev/null 2>&1; then TMO="timeout ` + pasarguardInstallScriptTimeout + `"; fi; ` +
		`$TMO $SUDO bash "$SCRIPT" install --name ` + nodeName + ` --yes </dev/null; ` +
		`STATUS=$?; rm -f "$SCRIPT"; exit $STATUS'`
	return command, nil
}

// RegistrationInfoCommand reads the values the PasarGuard panel needs to
// register the node: the API key from /opt/<name>/.env and the SSL
// certificate from /var/lib/<name>/certs/ssl_cert.pem.
func (PasarGuardProvider) RegistrationInfoCommand(nodeName string) (string, error) {
	if err := ValidateNodeName(nodeName); err != nil {
		return "", err
	}
	command := `sh -c '` + sudoPreamble +
		`printf "=ENVSTART=\n"; $SUDO cat /opt/` + nodeName + `/.env 2>/dev/null || true; printf "\n=ENVEND=\n"; ` +
		`printf "=CERTSTART=\n"; $SUDO cat /var/lib/` + nodeName + `/certs/ssl_cert.pem 2>/dev/null || true; printf "\n=CERTEND=\n"; ` +
		`true'`
	return command, nil
}

// RegistrationInfo carries everything needed to register a PasarGuard node in
// the panel.  It lives only in memory (install job store) — the API key and
// certificate are never persisted by Nodexia.
type RegistrationInfo struct {
	NodeName    string
	NodeIP      string
	ServicePort string
	Protocol    string
	APIKey      string
	Certificate string
}

// ParseRegistrationInfo parses the RegistrationInfoCommand output.  The bool
// result reports whether the node configuration was actually found.
func ParseRegistrationInfo(nodeName, output string) (RegistrationInfo, bool) {
	lines := strings.Split(output, "\n")
	info := RegistrationInfo{NodeName: nodeName}

	envLines, _ := markerSection(lines, "=ENVSTART=", "=ENVEND=")
	env := parseEnvFile(envLines)
	info.APIKey = cleanEnvValue(env["API_KEY"])
	info.ServicePort = parsePortFromEnv(env, "SERVICE_PORT")
	if info.ServicePort == "" {
		info.ServicePort = pasarguardDefaultPort
	}
	info.Protocol = cleanEnvValue(env["SERVICE_PROTOCOL"])
	if info.Protocol == "" {
		info.Protocol = pasarguardDefaultProtocol
	}

	certLines, _ := markerSection(lines, "=CERTSTART=", "=CERTEND=")
	cert := strings.TrimSpace(strings.Join(certLines, "\n"))
	if strings.Contains(cert, "BEGIN CERTIFICATE") {
		info.Certificate = cert
	}

	return info, info.APIKey != ""
}
