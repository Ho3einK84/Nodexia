package nodes

import (
	"fmt"
	"regexp"
	"sort"
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
	// pasarguardUnresolvedVersion is persisted when neither the authoritative
	// startup log nor a pinned image tag identifies the running node release.
	// In particular, "latest" is a moving image alias, not a node version.
	pasarguardUnresolvedVersion = "unknown"
	// pasarguardDefaultProtocol matches the install script default (gRPC).
	pasarguardDefaultProtocol = "grpc"

	// pasarguardInstallScriptTimeout bounds the install script remotely: after a
	// successful install the script tails container logs with no opt-out, so GNU
	// timeout cuts it off; 124 is therefore tolerated and the readback that
	// follows decides success.
	pasarguardInstallScriptTimeout = "600"

	// pasarguardDefaultName is the upstream default instance name, used when the
	// form leaves the name blank — the same value the script would prompt with.
	pasarguardDefaultName = "pg-node"
)

var (
	pasarguardStartupVersionPattern = regexp.MustCompile(`^Starting Node: v([0-9]+(?:\.[0-9]+)*)$`)
	pasarguardImageTagPattern       = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	uuidPattern                     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

func (PasarGuardProvider) Type() string        { return pasarguardType }
func (PasarGuardProvider) DisplayName() string { return "PasarGuard" }

func (PasarGuardProvider) SupportsInstall() bool { return true }

// InstallChannels: the upstream script installs a pinned release by default and
// can be pointed at the moving dev image with --dev, so both are selectable.
func (PasarGuardProvider) InstallChannels() []InstallChannel {
	return []InstallChannel{
		{Key: channelStable, Label: "nodes.channel_stable", Enabled: true},
		{Key: channelDev, Label: "nodes.channel_dev", Enabled: true},
	}
}

// InstallDefaults is the pre-filled deploy form state for this provider.
func (PasarGuardProvider) InstallDefaults() InstallDefaults {
	return InstallDefaults{
		NodeName:    pasarguardDefaultName,
		ServicePort: pasarguardDefaultPort,
		Protocol:    pasarguardDefaultProtocol,
		Channel:     channelStable,
	}
}

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
		// Match the PasarGuard NODE specifically (image pasarguard/node, or the
		// pg-node marker) — NOT a bare "pasarguard", which also matches the
		// PasarGuard PANEL (image pasarguard/pasarguard at /opt/pasarguard). The
		// panel is a separate product and must never be reported as a node.
		`grep -Eqi "pasarguard/node|pg-node" "$dir/docker-compose.yml" 2>/dev/null || continue; ` +
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
		// Host PIDs of this container's processes, used to attribute host-visible
		// xray listeners (host networking) to this node specifically.
		`PIDS=""; if [ "$HAVE_DOCKER" -eq 1 ]; then PIDS="$($SUDO docker top "$cname" -eo pid 2>/dev/null | tr "\n" " " || true)"; fi; ` +
		`printf "=PIDS=%s=\n" "$PIDS"; ` +
		`printf "=ENVSTART=\n"; cat "$dir/.env" 2>/dev/null || true; printf "\n=ENVEND=\n"; ` +
		`printf "=PGNODEEND=\n"; ` +
		`done; ` +
		// Host listening sockets with owning PIDs (once), matched per instance
		// against its container PIDs to attribute host-networked xray inbounds.
		listenProbeCommand +
		`true'`
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
	PIDs          string // host PIDs of the container's processes (`docker top`)
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
	listenLines, _ := markerSection(lines, "=LISTENP=", "=LISTENPEND=")
	listenSockets := parseListenSockets(listenLines)

	snapshots := make([]Snapshot, 0, len(instances))
	seen := map[string]struct{}{}
	for _, inst := range instances {
		seen[strings.ToLower(inst.Name)] = struct{}{}
		// Claim the linked container too, so it is not surfaced again below as a
		// phantom orphan (the bug: /opt/pg-node's container "node" reappearing).
		if cn := strings.ToLower(strings.TrimSpace(inst.ContainerName)); cn != "" {
			seen[cn] = struct{}{}
		}
		snapshots = append(snapshots, p.buildSnapshot(inst, containers, dockerSeen, listenSockets, collectedAt))
	}

	// Containers running a PasarGuard NODE image without a matching /opt
	// directory (manual installs) are still surfaced, with config defaults.
	// The image match is node-specific so the PasarGuard PANEL container
	// (pasarguard/pasarguard, e.g. "pasarguard-pasarguard-1") is never mistaken
	// for a node, and a plain "node:NN" (Node.js) container is ignored too.
	for _, c := range containers {
		if !isPasarguardNodeImage(c.Image) {
			continue
		}
		if _, ok := seen[strings.ToLower(c.Name)]; ok {
			continue
		}
		seen[strings.ToLower(c.Name)] = struct{}{}
		snapshots = append(snapshots, p.buildSnapshot(pgInstance{Name: c.Name}, containers, dockerSeen, listenSockets, collectedAt))
	}

	return snapshots
}

// VersionDiscoveryCommand builds the read-only follow-up probe used by Collect
// after the regular PasarGuard discovery command. The first probe is what maps
// an install directory name (for example "pg-node") to its real Docker
// container name (for example "node"), so the log lookup cannot be assembled
// safely until that output has been parsed.
//
// One command resolves all discovered PasarGuard containers. Only grep's
// tightly constrained "Starting Node: v<digits-and-dots>" match is returned;
// arbitrary container log content never crosses the SSH boundary.
func (PasarGuardProvider) VersionDiscoveryCommand(discoveryOutput string) string {
	targets := pasarguardVersionTargets(discoveryOutput)
	if len(targets) == 0 {
		return ""
	}

	var command strings.Builder
	command.WriteString(`sh -c '`)
	command.WriteString(`if [ "$(id -u)" -eq 0 ]; then SUDO=""; elif sudo -n true 2>/dev/null; then SUDO="sudo -n"; else SUDO=""; fi; `)
	command.WriteString(`printf "=PGVERSIONS=\n"; `)
	command.WriteString(`if command -v docker >/dev/null 2>&1; then `)
	for _, target := range targets {
		command.WriteString(`line="$($SUDO docker logs "`)
		command.WriteString(target.ContainerName)
		command.WriteString(`" 2>&1 | grep -o "Starting Node: v[0-9][0-9.]*" | head -n 1 || true)"; `)
		command.WriteString(`[ -n "$line" ] && printf "%s\t%s\n" "`)
		command.WriteString(target.Name)
		command.WriteString(`" "$line"; `)
	}
	command.WriteString(`fi; printf "=PGVERSIONSEND=\n"'`)
	return command.String()
}

type pasarguardVersionTarget struct {
	Name          string
	ContainerName string
}

// pasarguardVersionTargets preserves the directory-to-container link from the
// discovery output and also includes manual/orphan node containers. Every name
// is validated before it is interpolated into a shell command.
func pasarguardVersionTargets(discoveryOutput string) []pasarguardVersionTarget {
	lines := strings.Split(discoveryOutput, "\n")
	containers, _ := parseDockerSection(lines)
	instances := parsePGInstances(lines)

	targets := make([]pasarguardVersionTarget, 0, len(instances))
	claimed := map[string]struct{}{}
	addTarget := func(name, containerName string) {
		name = strings.TrimSpace(name)
		containerName = strings.TrimSpace(containerName)
		if containerName == "" {
			containerName = name
		}
		claimed[strings.ToLower(name)] = struct{}{}
		claimed[strings.ToLower(containerName)] = struct{}{}
		if ValidateNodeName(name) != nil || ValidateNodeName(containerName) != nil {
			return
		}
		targets = append(targets, pasarguardVersionTarget{
			Name:          name,
			ContainerName: containerName,
		})
	}

	for _, instance := range instances {
		addTarget(instance.Name, instance.ContainerName)
	}

	// parseDockerSection intentionally returns a map. Sort its keys so the
	// generated command (and probe evidence shown in the UI) stays stable.
	containerKeys := make([]string, 0, len(containers))
	for key := range containers {
		containerKeys = append(containerKeys, key)
	}
	sort.Strings(containerKeys)
	for _, key := range containerKeys {
		container := containers[key]
		if !isPasarguardNodeImage(container.Image) {
			continue
		}
		if _, seen := claimed[strings.ToLower(container.Name)]; seen {
			continue
		}
		addTarget(container.Name, container.Name)
	}
	return targets
}

// ResolveVersions applies authoritative versions extracted from PasarGuard
// startup logs. Snapshots with no valid log match keep the image-tag fallback
// chosen by buildSnapshot.
func (PasarGuardProvider) ResolveVersions(snapshots []Snapshot, versionOutput string) []Snapshot {
	resolved := parsePasarguardVersions(versionOutput)
	out := append([]Snapshot(nil), snapshots...)
	for i := range out {
		if out[i].NodeType != pasarguardType {
			continue
		}
		version := resolved[strings.ToLower(strings.TrimSpace(out[i].ServiceName))]
		if version == "" {
			continue
		}
		out[i].Version = version
		out[i].Evidence = append(append([]string(nil), out[i].Evidence...), "Version from container startup log: "+version)
		out[i] = normalizeSnapshot(out[i])
	}
	return out
}

func parsePasarguardVersions(output string) map[string]string {
	section, _ := markerSection(strings.Split(output, "\n"), "=PGVERSIONS=", "=PGVERSIONSEND=")
	versions := make(map[string]string)
	for _, raw := range section {
		name, logLine, ok := strings.Cut(raw, "\t")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			continue
		}
		version := parsePasarguardStartupVersion(logLine)
		if version == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := versions[key]; !exists {
			versions[key] = version
		}
	}
	return versions
}

// parsePasarguardStartupVersion accepts only the exact startup message emitted
// by the node and returns the numeric release after "v". Anchoring the match
// prevents malformed or unrelated log text from becoming a displayed version.
func parsePasarguardStartupVersion(line string) string {
	match := pasarguardStartupVersionPattern.FindStringSubmatch(strings.TrimSpace(line))
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

// isPasarguardNodeImage reports whether a Docker image reference belongs to a
// PasarGuard node. It matches the official image (pasarguard/node, including
// registry-prefixed mirrors like ghcr.io/pasarguard/node) and the pg-node
// marker, while deliberately rejecting the PasarGuard panel image
// (pasarguard/pasarguard) and unrelated images such as a bare node:NN runtime.
func isPasarguardNodeImage(image string) bool {
	lower := strings.ToLower(image)
	return strings.Contains(lower, "pasarguard/node") || strings.Contains(lower, "pg-node")
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
		case strings.HasPrefix(line, "=PIDS=") && strings.HasSuffix(line, "="):
			current.PIDs = strings.TrimSuffix(strings.TrimPrefix(line, "=PIDS="), "=")
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

func (PasarGuardProvider) buildSnapshot(inst pgInstance, containers map[string]dockerEntry, dockerSeen bool, listenSockets []listenSocket, collectedAt time.Time) Snapshot {
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
		version = pasarguardImageVersion(container.Image)
	}
	if version == "" {
		version = pasarguardImageVersion(composeImage(inst.ComposeLine))
	}
	if version == "" {
		version = pasarguardUnresolvedVersion
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
	// Published container ports (bridge networking) plus the container's own
	// host-visible listeners (host networking), minus the management ports.
	xrayPorts := uniqueStrings(append(
		containerXrayPorts(container.Ports, activePorts),
		xrayPortsForPIDs(listenSockets, parsePIDs(inst.PIDs), servicePort, apiPort)...,
	))
	sortPortsNumeric(xrayPorts)

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

// pasarguardImageVersion provides the graceful fallback when the startup log
// cannot be read. A non-empty pinned tag remains useful evidence; "latest" is
// deliberately rejected because it does not identify a release.
func pasarguardImageVersion(image string) string {
	// A digest is not an image tag. Preserve a real tag that precedes one
	// (repo/node:v1@sha256:...), but do not mistake the digest suffix or a
	// registry port for a version.
	reference := strings.TrimSpace(strings.SplitN(image, "@", 2)[0])
	if strings.LastIndex(reference, ":") <= strings.LastIndex(reference, "/") {
		return ""
	}
	tag := strings.TrimSpace(extractImageTag(reference))
	if tag == "" || strings.EqualFold(tag, "latest") || !pasarguardImageTagPattern.MatchString(tag) {
		return ""
	}
	return tag
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
// operation non-interactive: -n stops up/restart/logs from tailing forever.
//
// uninstall runs without --yes and is answered explicitly instead (see
// ActionCommand): the script asks two questions — "uninstall node?" and "remove
// data files?" — so a literal answer sequence is deterministic where --yes
// depends on the script having pre-seeded REPLY itself.
var pasarguardOps = map[string]string{
	"start":       "up -n",
	"stop":        "down",
	"restart":     "restart -n",
	"status":      "status",
	"logs":        "logs --no-follow",
	"update":      "update --yes",
	"core-update": "core-update --yes",
	"renew-cert":  "renew-cert --yes",
	"uninstall":   "uninstall",
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
	// exception: it answers both prompts explicitly for a complete removal.
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

// InstallConfig carries the pre-install choices the panel collects. Every field
// maps to a native flag of the upstream `install` subcommand, so the installer
// never has to be answered through positional stdin.
type InstallConfig struct {
	NodeName    string
	Channel     string // "stable" | "dev"
	ServicePort string // numeric, defaults to 62050 when empty
	APIPort     string // numeric, optional
	Protocol    string // "rest" or "grpc"
	APIKey      string // optional UUID; auto-generated by the script when empty
	SANEntries  string // optional comma separated extra certificate SANs
}

// Normalize fills defaults and validates each field, returning a cleaned copy.
func (c InstallConfig) Normalize() (InstallConfig, error) {
	out := InstallConfig{
		NodeName:    strings.TrimSpace(c.NodeName),
		Channel:     strings.ToLower(strings.TrimSpace(c.Channel)),
		ServicePort: strings.TrimSpace(c.ServicePort),
		APIPort:     strings.TrimSpace(c.APIPort),
		Protocol:    strings.ToLower(strings.TrimSpace(c.Protocol)),
		APIKey:      strings.TrimSpace(c.APIKey),
		SANEntries:  strings.TrimSpace(c.SANEntries),
	}
	if out.NodeName == "" {
		out.NodeName = pasarguardDefaultName
	}
	if err := ValidateNodeName(out.NodeName); err != nil {
		return InstallConfig{}, err
	}
	if out.Channel == "" {
		out.Channel = channelStable
	}
	if out.ServicePort == "" {
		out.ServicePort = pasarguardDefaultPort
	}
	if out.Protocol == "" {
		out.Protocol = pasarguardDefaultProtocol
	}
	if !isValidPort(out.ServicePort) {
		return InstallConfig{}, fmt.Errorf("nodes: invalid service port %q", c.ServicePort)
	}
	if out.APIPort != "" && !isValidPort(out.APIPort) {
		return InstallConfig{}, fmt.Errorf("nodes: invalid API port %q", c.APIPort)
	}
	if out.APIPort != "" && out.APIPort == out.ServicePort {
		return InstallConfig{}, fmt.Errorf("nodes: service and API ports must differ")
	}
	if out.Protocol != "rest" && out.Protocol != "grpc" {
		return InstallConfig{}, fmt.Errorf("nodes: invalid protocol %q (use rest or grpc)", c.Protocol)
	}
	if out.APIKey != "" && !uuidPattern.MatchString(out.APIKey) {
		return InstallConfig{}, fmt.Errorf("nodes: API key must be a valid UUID")
	}
	if out.SANEntries != "" && !sanEntriesPattern.MatchString(out.SANEntries) {
		return InstallConfig{}, fmt.Errorf("nodes: invalid SAN entries %q", c.SANEntries)
	}
	return out, nil
}

// normalizeInstallInput validates the raw install form fields and returns the
// resolved InstallConfig plus field-keyed validation errors (i18n keys).
func (PasarGuardProvider) normalizeInstallInput(in InstallFormInput) (InstallConfig, ValidationErrors) {
	errs := ValidationErrors{}

	if name := strings.TrimSpace(in.NodeName); name != "" {
		if err := ValidateNodeName(name); err != nil {
			errs["node_name"] = "nodes.err_node_name"
		}
	}
	if port := strings.TrimSpace(in.ServicePort); port != "" && !isValidPort(port) {
		errs["service_port"] = "nodes.err_port_range"
	}
	if port := strings.TrimSpace(in.APIPort); port != "" {
		if !isValidPort(port) {
			errs["api_port"] = "nodes.err_port_range"
		} else if port == strings.TrimSpace(in.ServicePort) {
			errs["api_port"] = "nodes.err_ports_equal"
		}
	}
	if proto := strings.ToLower(strings.TrimSpace(in.Protocol)); proto != "" && proto != "rest" && proto != "grpc" {
		errs["protocol"] = "nodes.err_protocol"
	}
	if key := strings.TrimSpace(in.APIKey); key != "" && !uuidPattern.MatchString(key) {
		errs["api_key"] = "nodes.err_api_key"
	}
	if san := strings.TrimSpace(in.SANEntries); san != "" && !sanEntriesPattern.MatchString(san) {
		errs["san_entries"] = "nodes.err_san_entries"
	}
	if errs.HasAny() {
		return InstallConfig{}, errs
	}

	config, err := InstallConfig{
		NodeName:    in.NodeName,
		Channel:     in.Channel,
		ServicePort: in.ServicePort,
		APIPort:     in.APIPort,
		Protocol:    in.Protocol,
		APIKey:      in.APIKey,
		SANEntries:  in.SANEntries,
	}.Normalize()
	if err != nil {
		errs["service_port"] = "nodes.err_port_range"
		return InstallConfig{}, errs
	}
	return config, errs
}

// InstallCommand downloads and runs the official PasarGuard install script for
// a new instance named nodeName.
//
// The upstream `install` subcommand accepts every choice as a native flag, so
// the old positional-stdin contract — and the prompt-counting guard that
// policed it — is gone:
//
//	-y                     non-interactive; auto-confirms the override prompt and
//	                       answers every remaining prompt with its default
//	--override             reinstall over an existing /opt/<name>
//	--self-signed          generate the TLS certificate instead of prompting
//	--service-port <p>     pin the node service port
//	--no-install-service   skip the systemd unit; Nodexia drives docker/compose
//	--install-service      install the unit (required for --api-port)
//	--use-rest|--use-grpc  protocol selection (flag presence, no value)
//	--api-key <uuid>       pin the API key instead of auto-generating one
//	--api-port <p>         pin the service API port
//	--san-entries <list>   extra certificate SANs
//	--dev                  install the moving dev image instead of a release
//
// The API port is only read by the node's systemd service install path, so
// requesting one implies the service unit; without an API port the service is
// skipped and the node runs purely from compose, which is what Nodexia's own
// actions drive.
func (PasarGuardProvider) InstallCommand(cfg InstallConfig) (string, error) {
	normalized, err := cfg.Normalize()
	if err != nil {
		return "", err
	}

	var flags strings.Builder
	flags.WriteString(` --name ` + normalized.NodeName)
	flags.WriteString(` -y --override --self-signed`)
	flags.WriteString(` --service-port ` + normalized.ServicePort)
	if normalized.APIPort != "" {
		flags.WriteString(` --install-service --api-port ` + normalized.APIPort)
	} else {
		flags.WriteString(` --no-install-service`)
	}
	if normalized.Protocol == "rest" {
		flags.WriteString(` --use-rest`)
	} else {
		flags.WriteString(` --use-grpc`)
	}
	if normalized.APIKey != "" {
		flags.WriteString(` --api-key ` + normalized.APIKey)
	}
	if normalized.SANEntries != "" {
		flags.WriteString(` --san-entries ` + normalized.SANEntries)
	}
	if normalized.Channel == channelDev {
		flags.WriteString(` --dev`)
	}

	command := `sh -c '` + sudoPreamble +
		stageMarker(stageInitializing) +
		strings.ReplaceAll(downloadPreamble, "URL", pasarguardScriptURL) +
		// The script's own dependency bootstrap (docker, compose, yq, jq) runs
		// between the download and the deploy, so the marker names that phase.
		stageMarker(stageDownloading) +
		`TMO=""; if command -v timeout >/dev/null 2>&1; then TMO="timeout ` + pasarguardInstallScriptTimeout + `"; fi; ` +
		// The install is the deploy phase. The script announces the node and then
		// tails container logs with no opt-out, so the tail lives under this
		// marker and the runner verifies the result afterwards.
		stageMarker(stageDeploying) +
		`$TMO $SUDO bash "$SCRIPT" install` + flags.String() + ` </dev/null; ` +
		`STATUS=$?; rm -f "$SCRIPT"; ` +
		// The script runs under `set -e` and dies without a message on the first
		// failed command; emit the exact exit code so the stream never ends
		// silently. 124 is GNU timeout cutting off the post-install log tail and
		// is treated as success by the runner, so it is not flagged.
		`if [ "$STATUS" -ne 0 ] && [ "$STATUS" -ne 124 ]; then echo "[pg-node install script exited with status $STATUS]" >&2; fi; ` +
		`exit $STATUS'`
	return command, nil
}

// BuildInstallPlan assembles the PasarGuard install procedure: one streamed
// stage running the official script, then a readback of the API key and
// certificate the panel needs for registration.
func (p PasarGuardProvider) BuildInstallPlan(in InstallFormInput) (InstallPlan, ValidationErrors) {
	config, errs := p.normalizeInstallInput(in)
	if errs.HasAny() {
		return InstallPlan{}, errs
	}

	installCmd, err := p.InstallCommand(config)
	if err != nil {
		return InstallPlan{}, ValidationErrors{"node_name": "nodes.err_node_name"}
	}
	infoCmd, err := p.RegistrationInfoCommand(config.NodeName)
	if err != nil {
		return InstallPlan{}, ValidationErrors{"node_name": "nodes.err_node_name"}
	}

	plan := InstallPlan{
		Stages: []InstallStage{
			{
				Key:             stageDownloading,
				StartLog:        fmt.Sprintf("\n[deploying PasarGuard node %q — channel %s, service port %s, protocol %s]\n", config.NodeName, config.Channel, config.ServicePort, config.Protocol),
				Command:         installCmd,
				Timeout:         installCommandTimeout,
				TolerateTimeout: true,
			},
		},
		Readback: InstallReadback{
			Command: infoCmd,
			Parse: func(stdout string) (RegistrationInfo, bool) {
				return ParseRegistrationInfo(config.NodeName, stdout)
			},
			NotFoundMessage: fmt.Sprintf("Install did not produce /opt/%s/.env — inspect the output above for the script error.", config.NodeName),
		},
	}
	return plan, nil
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
	APIPort     string
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
	info.APIPort = parsePortFromEnv(env, "API_PORT")
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
