package nodes

import (
	"fmt"
	"regexp"
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
//     references PasarGuard, including its compose image line, the actual
//     container name ("=CONTAINER="), an authoritative "=STATE=" from
//     `docker inspect`, and its .env.
//
// The install directory name and the running container name differ: the default
// install lives in /opt/pg-node but its container is "node" (the compose service
// name), so inspecting by the directory name finds nothing and the node would be
// reported twice — once as the dead "pg-node" dir and once as the live "node"
// container surfaced by the orphan fallback. We therefore read the real name
// from the compose file (container_name:, else the first service under
// services:) and inspect by that, linking the install to its container.
//
// Docker is queried through passwordless sudo (matching the management
// actions): node hosts commonly require root for the Docker socket, and
// without it discovery would see no containers and wrongly report every node
// as stopped.
func (PasarGuardProvider) DiscoveryCommand() string {
	return `sh -c '` +
		`if [ "$(id -u)" -eq 0 ]; then SUDO=""; elif sudo -n true 2>/dev/null; then SUDO="sudo -n"; else SUDO=""; fi; ` +
		`HAVE_DOCKER=0; command -v docker >/dev/null 2>&1 && HAVE_DOCKER=1; ` +
		`if [ "$HAVE_DOCKER" -eq 1 ]; then ` +
		`printf "=DOCKER=\n"; ` +
		`$SUDO docker ps -a --format "{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}" 2>/dev/null || true; ` +
		`printf "=DOCKEREND=\n"; ` +
		`fi; ` +
		`for dir in /opt/*/; do ` +
		`[ -f "$dir/docker-compose.yml" ] || continue; ` +
		`grep -Eqi "pasarguard|pg-node" "$dir/docker-compose.yml" 2>/dev/null || continue; ` +
		`name="${dir%/}"; name="${name##*/}"; ` +
		// Resolve the actual container name: prefer an explicit container_name:,
		// else the first service key under services:, else the directory name.
		`cname="$(grep -iE "^[[:space:]]*container_name:" "$dir/docker-compose.yml" 2>/dev/null | head -n 1 | sed -e "s/.*container_name:[[:space:]]*//" -e "s/[\"]//g" | tr -d "[:space:]")"; ` +
		`if [ -z "$cname" ]; then cname="$(sed -n "/^[[:space:]]*services:/,/^[^[:space:]#]/p" "$dir/docker-compose.yml" 2>/dev/null | sed -n -e "s/^[[:space:]]\{1,\}\([A-Za-z0-9._-]\{1,\}\):[[:space:]]*$/\1/p" | head -n 1)"; fi; ` +
		`[ -n "$cname" ] || cname="$name"; ` +
		`printf "=PGNODE=%s=\n" "$name"; ` +
		`printf "=CONTAINER=%s=\n" "$cname"; ` +
		`printf "=IMAGE=%s=\n" "$(grep -i "image:" "$dir/docker-compose.yml" 2>/dev/null | head -n 1)"; ` +
		`[ -d "/var/lib/$name" ] && printf "=DATADIR=/var/lib/%s=\n" "$name"; ` +
		`state=""; ` +
		`[ "$HAVE_DOCKER" -eq 1 ] && state="$($SUDO docker inspect -f "{{.State.Status}}" "$cname" 2>/dev/null)"; ` +
		`printf "=STATE=%s=\n" "$state"; ` +
		`printf "=ENVSTART=\n"; cat "$dir/.env" 2>/dev/null || true; printf "\n=ENVEND=\n"; ` +
		`printf "=PGNODEEND=\n"; ` +
		`done; true'`
}

// pgInstance is the parsed evidence for one PasarGuard install directory.
type pgInstance struct {
	Name string
	// ContainerName is the real Docker container name from the compose file
	// (container_name: or the service key), which differs from the install
	// directory name — e.g. /opt/pg-node runs container "node". Empty falls back
	// to Name. It is what `docker inspect` and the orphan fallback key on.
	ContainerName string
	ComposeLine   string
	DataDir       string
	State         string // `docker inspect` .State.Status (running/exited/…), or ""
	EnvLines      []string
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
		// Claim the linked container too, so it is not surfaced again below as a
		// phantom orphan (the bug: /opt/pg-node's container "node" reappearing).
		if cn := strings.ToLower(strings.TrimSpace(inst.ContainerName)); cn != "" {
			seen[cn] = struct{}{}
		}
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
		case strings.HasPrefix(line, "=CONTAINER=") && strings.HasSuffix(line, "="):
			current.ContainerName = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "=CONTAINER="), "="))
		case strings.HasPrefix(line, "=IMAGE=") && strings.HasSuffix(line, "="):
			current.ComposeLine = strings.TrimSuffix(strings.TrimPrefix(line, "=IMAGE="), "=")
		case strings.HasPrefix(line, "=DATADIR=") && strings.HasSuffix(line, "="):
			current.DataDir = strings.TrimSuffix(strings.TrimPrefix(line, "=DATADIR="), "=")
		case strings.HasPrefix(line, "=STATE=") && strings.HasSuffix(line, "="):
			current.State = strings.TrimSuffix(strings.TrimPrefix(line, "=STATE="), "=")
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
	// Look the container up by its real name, not the install directory name.
	lookup := strings.ToLower(strings.TrimSpace(inst.ContainerName))
	if lookup == "" {
		lookup = strings.ToLower(inst.Name)
	}
	container, hasContainer := containers[lookup]

	servicePort := parsePortFromEnv(env, "SERVICE_PORT")
	if servicePort == "" {
		servicePort = pasarguardDefaultPort
	}
	apiPort := parsePortFromEnv(env, "API_PORT")
	protocol := cleanEnvValue(env["SERVICE_PROTOCOL"])
	if protocol == "" {
		protocol = pasarguardDefaultProtocol
	}

	// DataDir is only trusted when the probe actually found /var/lib/<name>
	// (=DATADIR=). We never guess the path: a missing directory must read as
	// empty ("-" in the UI), not as a path that does not exist on the host.
	dataDir := inst.DataDir

	version := ""
	if hasContainer {
		version = extractImageTag(container.Image)
	}
	if version == "" {
		version = extractImageTag(composeImage(inst.ComposeLine))
	}

	health := pgHealth(inst.State, hasContainer, container.Status)

	evidence := []string{
		fmt.Sprintf("Install directory: /opt/%s (docker-compose.yml)", inst.Name),
	}
	if len(env) > 0 {
		evidence = append(evidence, fmt.Sprintf("Config: /opt/%s/.env (service port %s, protocol %s)", inst.Name, servicePort, protocol))
	}
	if strings.TrimSpace(inst.State) != "" {
		evidence = append(evidence, "Container state: "+strings.TrimSpace(inst.State))
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

// pgHealth resolves a node's health. The authoritative source is the
// `docker inspect` state captured during discovery; the `docker ps` listing is
// a fallback, and only when neither is available do we report "unknown"
// (never a false "stopped").
func pgHealth(inspectState string, hasContainer bool, containerStatus string) string {
	switch strings.ToLower(strings.TrimSpace(inspectState)) {
	case "running", "restarting":
		return "running"
	case "created", "exited", "dead", "paused", "removing":
		return "stopped"
	}
	if hasContainer {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(containerStatus)), "up") {
			return "running"
		}
		return "stopped"
	}
	return "unknown"
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
	// uninstall runs WITHOUT --yes: the script's uninstall confirm is broken
	// under --yes (it sets REPLY="" then requires ^[Yy]$, so --yes always prints
	// "Aborted" and exits 1). ActionCommand pipes "y" to its prompts instead.
	"uninstall": "uninstall",
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

	// Most actions take no input, so stdin is /dev/null. uninstall is the
	// exception: it runs without --yes (see pasarguardOps) and the script asks
	// two questions — "uninstall node?" and "remove data files?" — so pipe "y"
	// to both for a clean, complete removal.
	invocation := `$SUDO "$PG_CLI" --name ` + nodeName + ` ` + op
	if action.Key == "uninstall" {
		invocation = `printf "y\ny\n" | ` + invocation
	} else {
		invocation += ` </dev/null`
	}

	command := `sh -c '` + sudoPreamble +
		`if command -v pg-node >/dev/null 2>&1; then PG_CLI="pg-node"; ` +
		`elif command -v ` + nodeName + ` >/dev/null 2>&1; then PG_CLI="` + nodeName + `"; ` +
		`else echo "pg-node CLI not found on this server" >&2; exit 86; fi; ` +
		invocation + `'`
	return command, action.Timeout, nil
}

// ── Installation ──────────────────────────────────────────────────────────────

// pasarguardInstallScriptTimeout bounds the install script on the remote side:
// after a successful install the script tails container logs with no opt-out,
// so GNU timeout cuts it off and the runner verifies the result by reading the
// installed configuration (RegistrationInfoCommand).
const pasarguardInstallScriptTimeout = "600"

// InstallCommand downloads and runs the official PasarGuard install script for
// a new instance named nodeName, feeding cfg.ServicePort to the port prompt.
//
// We do NOT pass --yes to the script: --yes locks the port to 62050 regardless
// of what is free and fails with "Port 62050 is already in use" when another
// node instance already occupies that port. Without --yes the script reads its
// prompts from stdin, and because the script runs under `set -e`, a `read` that
// hits EOF returns non-zero and aborts the whole install with no error message.
// So stdin MUST answer every prompt, in the script's exact order, each newline
// terminated — otherwise the install dies silently right after the prompt we
// stop short of (historically: right after "No API Key provided…").
//
// The official install_command / install_node prompt order (AUTO_CONFIRM off):
//  1. "use your own public certificate?"  -> "" (default: self-signed cert)
//  2. "add additional SAN entries?"       -> "" (default: keep current SANs;
//     prompted by gen_self_signed_cert on the self-signed path)
//  3. "enter your API Key"                -> "" (default: auto-generated UUID)
//  4. "use REST protocol instead?"        -> "" (default: gRPC)
//  5. "enter the SERVICE_PORT"            -> cfg.ServicePort
//  6. "install the systemd service?"      -> "n" (Nodexia drives the node via
//     the pg-node CLI / docker-compose, never systemd; answering "n" also
//     avoids the service's own API_PORT prompt and the require_systemd exit on
//     hosts without systemd)
//
// Protocol, API key and API port are deliberately left at their script defaults
// here and overwritten afterwards by ConfigureCommand patching /opt/<name>/.env.
func (PasarGuardProvider) InstallCommand(nodeName string, cfg InstallConfig) (string, error) {
	if err := ValidateNodeName(nodeName); err != nil {
		return "", err
	}
	normalized, err := cfg.Normalize()
	if err != nil {
		return "", err
	}
	servicePort := normalized.ServicePort
	command := `sh -c '` + sudoPreamble +
		`SCRIPT="$(mktemp /tmp/nodexia-pg-node.XXXXXX)" || exit 1; ` +
		`if command -v curl >/dev/null 2>&1; then curl -fsSL ` + pasarguardScriptURL + ` -o "$SCRIPT" || { echo "download failed" >&2; rm -f "$SCRIPT"; exit 85; }; ` +
		`elif command -v wget >/dev/null 2>&1; then wget -qO "$SCRIPT" ` + pasarguardScriptURL + ` || { echo "download failed" >&2; rm -f "$SCRIPT"; exit 85; }; ` +
		`else echo "curl or wget is required to install" >&2; rm -f "$SCRIPT"; exit 85; fi; ` +
		`TMO=""; if command -v timeout >/dev/null 2>&1; then TMO="timeout ` + pasarguardInstallScriptTimeout + `"; fi; ` +
		`printf "\n\n\n\n` + servicePort + `\nn\n" | $TMO $SUDO bash "$SCRIPT" install --name ` + nodeName + `; ` +
		`STATUS=$?; rm -f "$SCRIPT"; ` +
		// The script runs under `set -e` and dies without a message on the
		// first failed command; emit the exact exit code so the stream never
		// ends silently. 124 is GNU timeout cutting off the post-install log
		// tail and is treated as success by the runner, so it is not flagged.
		`if [ "$STATUS" -ne 0 ] && [ "$STATUS" -ne 124 ]; then echo "[pg-node install script exited with status $STATUS]" >&2; fi; ` +
		`exit $STATUS'`
	return command, nil
}

// InstallConfig carries the pre-install choices the panel collects.
// ServicePort is piped to the script's interactive port prompt so the node
// binds the correct port from the first start. Protocol and APIKey are applied
// after install via ConfigureCommand (the script's interactive defaults — grpc
// and auto-generated key — are used during the install run itself).
type InstallConfig struct {
	ServicePort string // numeric, defaults to 62050 when empty
	APIPort     string // numeric, optional
	Protocol    string // "rest" or "grpc"
	APIKey      string // optional UUID; auto-generated by the script when empty
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Normalize fills defaults and validates each field, returning a cleaned copy.
func (c InstallConfig) Normalize() (InstallConfig, error) {
	out := InstallConfig{
		ServicePort: strings.TrimSpace(c.ServicePort),
		APIPort:     strings.TrimSpace(c.APIPort),
		Protocol:    strings.ToLower(strings.TrimSpace(c.Protocol)),
		APIKey:      strings.TrimSpace(c.APIKey),
	}
	if out.ServicePort == "" {
		out.ServicePort = pasarguardDefaultPort
	}
	if out.Protocol == "" {
		out.Protocol = pasarguardDefaultProtocol
	}
	if validPort(out.ServicePort) == "" {
		return InstallConfig{}, fmt.Errorf("nodes: invalid service port %q", c.ServicePort)
	}
	if out.APIPort != "" && validPort(out.APIPort) == "" {
		return InstallConfig{}, fmt.Errorf("nodes: invalid API port %q", c.APIPort)
	}
	if out.Protocol != "rest" && out.Protocol != "grpc" {
		return InstallConfig{}, fmt.Errorf("nodes: invalid protocol %q (use rest or grpc)", c.Protocol)
	}
	if out.APIKey != "" && !uuidPattern.MatchString(out.APIKey) {
		return InstallConfig{}, fmt.Errorf("nodes: API key must be a valid UUID")
	}
	return out, nil
}

// validPort returns the canonical port string if value is a port in 1..65535,
// otherwise "".
func validPort(value string) string {
	return parsePortFromEnv(map[string]string{"p": value}, "p")
}

// normalizeInstallInput validates the raw install form fields and returns the
// resolved InstallConfig plus field-keyed validation errors (empty when valid).
func (PasarGuardProvider) normalizeInstallInput(in installFormInput) (InstallConfig, ValidationErrors) {
	errs := ValidationErrors{}

	if port := strings.TrimSpace(in.ServicePort); port != "" && validPort(port) == "" {
		errs["service_port"] = "Enter a port between 1 and 65535."
	}
	if port := strings.TrimSpace(in.APIPort); port != "" && validPort(port) == "" {
		errs["api_port"] = "Enter a port between 1 and 65535, or leave blank."
	}
	if proto := strings.ToLower(strings.TrimSpace(in.Protocol)); proto != "" && proto != "rest" && proto != "grpc" {
		errs["protocol"] = "Choose REST or gRPC."
	}
	if key := strings.TrimSpace(in.APIKey); key != "" && !uuidPattern.MatchString(key) {
		errs["api_key"] = "Provide a valid UUID, or leave blank to auto-generate."
	}
	if errs.HasAny() {
		return InstallConfig{}, errs
	}

	config, err := InstallConfig{
		ServicePort: in.ServicePort,
		APIPort:     in.APIPort,
		Protocol:    in.Protocol,
		APIKey:      in.APIKey,
	}.Normalize()
	if err != nil {
		errs["service_port"] = err.Error()
		return InstallConfig{}, errs
	}
	return config, errs
}

// ConfigureCommand patches /opt/<name>/.env with the chosen ports/protocol/key
// (deleting any prior commented or active line for each key, then appending the
// desired value — mirroring the install script's own .env handling) and
// restarts the node through the official CLI so the changes take effect.
// Every interpolated value is validated, so the command is injection-safe.
func (p PasarGuardProvider) ConfigureCommand(nodeName string, cfg InstallConfig) (string, time.Duration, error) {
	if err := ValidateNodeName(nodeName); err != nil {
		return "", 0, err
	}
	normalized, err := cfg.Normalize()
	if err != nil {
		return "", 0, err
	}

	var sets strings.Builder
	// writeKey drops any existing line (commented "# KEY =" or active "KEY =")
	// then appends the canonical assignment. value is wrapped in escaped double
	// quotes so no single quote ever appears inside the outer sh -c '...' — all
	// values are validated (numeric / rest|grpc / UUID), so this is safe.
	writeKey := func(key, value string) {
		sets.WriteString(`sed -i "/^# *` + key + ` *=/d; /^` + key + ` *=/d" "$ENV"; `)
		sets.WriteString(`printf "%s\n" "` + key + `= ` + value + `" >>"$ENV"; `)
	}
	writeKey("SERVICE_PORT", normalized.ServicePort)
	// The .env stores the protocol quoted: SERVICE_PROTOCOL= "grpc".
	writeKey("SERVICE_PROTOCOL", `\"`+normalized.Protocol+`\"`)
	if normalized.APIPort != "" {
		writeKey("API_PORT", normalized.APIPort)
	}
	if normalized.APIKey != "" {
		writeKey("API_KEY", normalized.APIKey)
	}

	command := `sh -c '` + sudoPreamble +
		`ENV=/opt/` + nodeName + `/.env; ` +
		`if [ ! -f "$ENV" ]; then echo "/opt/` + nodeName + `/.env not found" >&2; exit 86; fi; ` +
		sets.String() +
		`if command -v pg-node >/dev/null 2>&1; then PG_CLI="pg-node"; ` +
		`elif command -v ` + nodeName + ` >/dev/null 2>&1; then PG_CLI="` + nodeName + `"; ` +
		`else echo "pg-node CLI not found on this server" >&2; exit 86; fi; ` +
		`$SUDO "$PG_CLI" --name ` + nodeName + ` restart -n </dev/null'`
	return command, 5 * time.Minute, nil
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
