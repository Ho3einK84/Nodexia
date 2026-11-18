package nodes

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// RebeccaProvider drives Rebecca nodes (https://github.com/rebeccapanel/Rebecca-node).
//
// A single instance lives under /opt/rebecca-node (configuration in .env,
// version metadata in .binary-release.json) with data under /var/lib/rebecca-node.
// Management goes through the official `rebecca-node` CLI. Installs run in
// binary mode (native systemd service) on either the stable or dev channel.
//
// The CLI is registered under the INSTANCE name (/usr/local/bin/<name>) by the
// install script, so a host can run several nodes side by side. Upstream reads
// REBECCA_NODE_APP_NAME to pick the target instance, so every action passes it
// explicitly — without it, `rebecca-node <op>` silently drives whichever
// instance it discovers first (or prompts when several exist).
type RebeccaProvider struct{}

const (
	rebeccaType = "rebecca-node"
	// Defaults written by the official rebecca-node install script.
	rebeccaDefaultServicePort = "62050"
	rebeccaDefaultAPIPort     = "62051"
	rebeccaDefaultProtocol    = "rest"

	// rebeccaInstallName is the default instance name (used when the install
	// form leaves the name blank).
	rebeccaInstallName = "rebecca-node"

	// rebeccaScriptURLStable / rebeccaScriptURLDev are the binary-flavored
	// install scripts, one per channel. The BINARY script is deliberate: the
	// script installs the `rebecca-node` management CLI as a copy of itself, so
	// running a docker-flavored script would leave a docker-flavored CLI behind
	// and every later `rebecca-node update`/action would abort with
	// "installation is in binary mode, but rebecca-node is the docker script".
	rebeccaScriptURLStable = "https://raw.githubusercontent.com/rebeccapanel/Rebecca/master/scripts/rebecca/rebecca-node-binary.sh"
	rebeccaScriptURLDev    = "https://raw.githubusercontent.com/rebeccapanel/Rebecca/dev/scripts/rebecca/rebecca-node-binary.sh"

	// rebeccaInstallScriptTimeout bounds the install script remotely. The binary
	// install downloads a binary and installs xray, so it has no infinite log
	// tail; the bound just backstops a hung download.
	rebeccaInstallScriptTimeout = "600"
)

// Install channel keys. The channel is data so enabling one is a one-line flip
// in InstallChannels plus its script URL, not a rewrite.
const (
	channelStable = "stable"
	channelDev    = "dev"
)

func (RebeccaProvider) Type() string        { return rebeccaType }
func (RebeccaProvider) DisplayName() string { return "Rebecca" }

func (RebeccaProvider) SupportsInstall() bool { return true }

// InstallChannels lists Rebecca's release channels. Both are installable: each
// maps to its own binary-flavored script on the matching git ref.
func (RebeccaProvider) InstallChannels() []InstallChannel {
	return []InstallChannel{
		{Key: channelStable, Label: "nodes.channel_stable", Enabled: true},
		{Key: channelDev, Label: "nodes.channel_dev", Enabled: true},
	}
}

// rebeccaScriptURL resolves the download URL for a channel.
func rebeccaScriptURL(channel string) string {
	if channel == channelDev {
		return rebeccaScriptURLDev
	}
	return rebeccaScriptURLStable
}

// rebeccaChannelEnabled reports whether the named channel currently installs.
func rebeccaChannelEnabled(channel string) bool {
	for _, c := range (RebeccaProvider{}).InstallChannels() {
		if c.Key == channel {
			return c.Enabled
		}
	}
	return false
}

// InstallDefaults is the pre-filled deploy form state for this provider.
func (RebeccaProvider) InstallDefaults() InstallDefaults {
	return InstallDefaults{
		NodeName:    rebeccaInstallName,
		ServicePort: rebeccaDefaultServicePort,
		APIPort:     rebeccaDefaultAPIPort,
		Channel:     channelStable,
	}
}

// DiscoveryCommand scans /opt for every Rebecca-node install and reads each
// instance's footprint in one pass: .env, .binary-release.json, the install
// mode marker, the systemd unit state, and the docker container state (the
// official script supports both modes). A host can run several instances
// (/opt/<name>), so it emits one "=REBECCANODE=<name>=" block per install.
func (RebeccaProvider) DiscoveryCommand() string {
	return `sh -c '` +
		`if [ "$(id -u)" -eq 0 ]; then SUDO=""; elif sudo -n true 2>/dev/null; then SUDO="sudo -n"; else SUDO=""; fi; ` +
		`HAVE_DOCKER=0; command -v docker >/dev/null 2>&1 && HAVE_DOCKER=1; ` +
		`if [ "$HAVE_DOCKER" -eq 1 ]; then ` +
		`printf "=DOCKER=\n"; ` +
		`$SUDO docker ps -a --format "{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}" 2>/dev/null || true; ` +
		`printf "=DOCKEREND=\n"; ` +
		`fi; ` +
		`for dir in /opt/*/; do ` +
		`base="${dir%/}"; ` +
		`name="${base##*/}"; ` +
		`is_node=0; cf=""; ` +
		`if [ -f "$base/.binary-release.json" ] && $SUDO grep -Eqi "\"image\"[^,]*rebecca-node" "$base/.binary-release.json" 2>/dev/null; then is_node=1; fi; ` +
		`for cand in "$base/docker-compose.yml" "$base/docker-compose.yaml" "$base/compose.yml" "$base/compose.yaml"; do ` +
		`[ -f "$cand" ] && $SUDO grep -Eqi "image:[^#]*rebecca-node" "$cand" 2>/dev/null && { cf="$cand"; is_node=1; break; }; ` +
		`done; ` +
		`[ "$is_node" -eq 1 ] || continue; ` +
		`printf "=REBECCANODE=%s=\n" "$name"; ` +
		`printf "=ENVSTART=\n"; $SUDO cat "$base/.env" 2>/dev/null || true; printf "\n=ENVEND=\n"; ` +
		`printf "=RELEASESTART=\n"; $SUDO cat "$base/.binary-release.json" 2>/dev/null || true; printf "\n=RELEASEEND=\n"; ` +
		`printf "=MODE=%s=\n" "$($SUDO cat "$base/.install-mode" 2>/dev/null)"; ` +
		`printf "=SERVICE=%s=\n" "$(systemctl is-active "$name" 2>/dev/null)"; ` +
		// Systemd cgroup v2 PIDs with $SUDO, falling back to cgroup v1 / MainPID descendants.
		`cg="$(systemctl show -p ControlGroup --value "$name" 2>/dev/null || true)"; ` +
		`PIDS=""; ` +
		`if [ -n "$cg" ]; then ` +
		`if $SUDO test -r "/sys/fs/cgroup$cg/cgroup.procs" 2>/dev/null; then PIDS="$($SUDO cat "/sys/fs/cgroup$cg/cgroup.procs" 2>/dev/null | tr "\n" " " || true)"; ` +
		`elif $SUDO test -r "/sys/fs/cgroup/systemd$cg/cgroup.procs" 2>/dev/null; then PIDS="$($SUDO cat "/sys/fs/cgroup/systemd$cg/cgroup.procs" 2>/dev/null | tr "\n" " " || true)"; fi; ` +
		`fi; ` +
		`if [ -z "$PIDS" ]; then ` +
		`mpid="$(systemctl show -p MainPID --value "$name" 2>/dev/null || true)"; ` +
		`if [ -n "$mpid" ] && [ "$mpid" != "0" ]; then PIDS="$mpid $($SUDO pgrep -P "$mpid" 2>/dev/null | tr "\n" " " || true)"; fi; ` +
		`fi; ` +
		`if [ "$HAVE_DOCKER" -eq 1 ]; then ` +
		`cname=""; ` +
		`if [ -n "$cf" ]; then ` +
		`cname="$(grep -iE "^[[:space:]]*container_name:" "$cf" 2>/dev/null | head -n 1 | sed -e "s/.*container_name:[[:space:]]*//" -e "s/[[:space:]]*#.*//" | tr -d "\047\" \t\r")"; ` +
		`if [ -z "$cname" ]; then cname="$(sed -n "/^[[:space:]]*services:/,/^[^[:space:]#]/p" "$cf" 2>/dev/null | sed -n -e "s/^[[:space:]]\{1,\}\([A-Za-z0-9._-]\{1,\}\):[[:space:]]*\(#.*\)\{0,1\}$/\1/p" | head -n 1)"; fi; ` +
		`fi; ` +
		`[ -n "$cname" ] || cname="$name"; ` +
		`for ccand in "$cname" "${name}-${cname}-1" "${name}_${cname}_1" "$name"; do ` +
		`if $SUDO docker inspect "$ccand" >/dev/null 2>&1; then cname="$ccand"; break; fi; ` +
		`done; ` +
		`printf "=CNAME=%s=\n" "$cname"; ` +
		`[ -n "$cf" ] && printf "=IMAGE=%s=\n" "$(grep -i "image:" "$cf" 2>/dev/null | head -n 1)"; ` +
		`printf "=CONTAINER=%s=\n" "$($SUDO docker ps -a -f "name=${cname}" --format "{{.Names}}\t{{.Status}}" 2>/dev/null | awk -F "\t" -v target="$cname" "\$1==target{print \$2; exit}")"; ` +
		`printf "=STATE=%s=\n" "$($SUDO docker inspect -f "{{.State.Status}}" "$cname" 2>/dev/null || true)"; ` +
		`printf "=HEALTH=%s=\n" "$($SUDO docker inspect -f "{{if .State.Health}}{{.State.Health.Status}}{{end}}" "$cname" 2>/dev/null || true)"; ` +
		`dver="$($SUDO docker inspect -f "{{index .Config.Labels \"org.opencontainers.image.version\"}}" "$cname" 2>/dev/null || true)"; ` +
		`[ -n "$dver" ] && printf "=DOCKERVERSION=%s=\n" "$dver"; ` +
		`dpids="$($SUDO docker top "$cname" -eo pid 2>/dev/null | tr "\n" " " || true)"; ` +
		`[ -n "$dpids" ] && PIDS="$PIDS $dpids"; ` +
		`fi; ` +
		`printf "=PIDS=%s=\n" "$PIDS"; ` +
		`printf "=REBECCANODEEND=\n"; ` +
		`done; ` +
		listenProbeCommand +
		`true'`
}

func (RebeccaProvider) ParseDiscovery(output string, collectedAt time.Time) []Snapshot {
	lines := strings.Split(output, "\n")
	containers, _ := parseDockerSection(lines)

	listenLines, _ := markerSection(lines, "=LISTENP=", "=LISTENPEND=")
	listenSockets := parseListenSockets(listenLines)

	var snapshots []Snapshot
	claimedContainers := map[string]struct{}{}
	var name string
	var block []string
	inBlock := false

	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(trimmed, "=REBECCANODE=") && strings.HasSuffix(trimmed, "="):
			name = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "=REBECCANODE="), "="))
			block = nil
			inBlock = true
		case trimmed == "=REBECCANODEEND=":
			if inBlock && name != "" {
				claimedContainers[strings.ToLower(name)] = struct{}{}
				if cname, _ := markerValue(block, "CNAME"); cname != "" {
					claimedContainers[strings.ToLower(cname)] = struct{}{}
				}
				if snap, ok := parseRebeccaInstance(name, block, containers, listenSockets, collectedAt); ok {
					snapshots = append(snapshots, snap)
				}
			}
			inBlock = false
			name = ""
			block = nil
		case inBlock:
			block = append(block, raw)
		}
	}

	// Surface standalone Docker Rebecca-node containers that do not live in /opt/<name>.
	containerKeys := make([]string, 0, len(containers))
	for key := range containers {
		containerKeys = append(containerKeys, key)
	}
	sort.Strings(containerKeys)
	for _, key := range containerKeys {
		c := containers[key]
		if !isRebeccaNodeImage(c.Image) {
			continue
		}
		if _, ok := claimedContainers[strings.ToLower(c.Name)]; ok {
			continue
		}
		claimedContainers[strings.ToLower(c.Name)] = struct{}{}
		block := []string{
			"=CNAME=" + c.Name + "=",
			"=MODE=docker=",
			"=CONTAINER=" + c.Status + "=",
			"=IMAGE=" + c.Image + "=",
		}
		if snap, ok := parseRebeccaInstance(c.Name, block, containers, listenSockets, collectedAt); ok {
			snapshots = append(snapshots, snap)
		}
	}

	return snapshots
}

func parseRebeccaInstance(name string, lines []string, containers map[string]dockerEntry, listenSockets []listenSocket, collectedAt time.Time) (Snapshot, bool) {
	envLines, _ := markerSection(lines, "=ENVSTART=", "=ENVEND=")
	env := parseEnvFile(envLines)

	servicePort := parsePortFromEnv(env, "SERVICE_PORT")
	if servicePort == "" {
		servicePort = rebeccaDefaultServicePort
	}
	apiPort := parsePortFromEnv(env, "XRAY_API_PORT")
	if apiPort == "" {
		apiPort = rebeccaDefaultAPIPort
	}
	protocol := cleanEnvValue(env["SERVICE_PROTOCOL"])
	if protocol == "" {
		protocol = rebeccaDefaultProtocol
	}
	dataDir := cleanEnvValue(env["REBECCA_DATA_DIR"])
	if dataDir == "" {
		dataDir = "/var/lib/" + name
	}

	releaseLines, _ := markerSection(lines, "=RELEASESTART=", "=RELEASEEND=")
	version, installModeFromRelease, image := parseRebeccaRelease(strings.Join(releaseLines, "\n"))

	if image != "" && !isRebeccaNodeImage(image) {
		return Snapshot{}, false
	}

	cname, _ := markerValue(lines, "CNAME")
	lookup := strings.ToLower(strings.TrimSpace(cname))
	if lookup == "" {
		lookup = strings.ToLower(strings.TrimSpace(name))
	}
	container, hasContainer := containers[lookup]

	installMode, _ := markerValue(lines, "MODE")
	installMode = strings.ToLower(strings.TrimSpace(installMode))
	if installMode == "" {
		installMode = installModeFromRelease
	}
	if installMode == "" {
		if hasContainer || cname != "" {
			installMode = "docker"
		} else {
			installMode = "binary"
		}
	}

	if version == "" {
		if dockerVer, _ := markerValue(lines, "DOCKERVERSION"); dockerVer != "" {
			version = normalizeNodeVersion(dockerVer)
		}
	}
	if version == "" && hasContainer {
		version = pasarguardImageVersion(container.Image)
	}
	if version == "" {
		if imgLine, _ := markerValue(lines, "IMAGE"); imgLine != "" {
			version = pasarguardImageVersion(composeImage(imgLine))
		}
	}

	serviceState, _ := markerValue(lines, "SERVICE")
	containerStatus, _ := markerValue(lines, "CONTAINER")
	if containerStatus == "" && hasContainer {
		containerStatus = container.Status
	}
	inspectState, _ := markerValue(lines, "STATE")
	healthCheckState, _ := markerValue(lines, "HEALTH")
	health := rebeccaHealthWithCheck(installMode, serviceState, inspectState, healthCheckState, containerStatus)

	appDir := "/opt/" + name
	evidence := []string{"Install directory: " + appDir}
	if len(env) > 0 {
		evidence = append(evidence, fmt.Sprintf("Config: %s/.env (service port %s, protocol %s)", appDir, servicePort, protocol))
	}
	if version != "" {
		evidence = append(evidence, "Version: "+version)
	}
	if serviceState != "" {
		evidence = append(evidence, "systemd "+name+": "+serviceState)
	}
	if containerStatus != "" {
		evidence = append(evidence, "Docker container "+lookup+": "+containerStatus)
	}

	confidence := "medium"
	if len(env) > 0 || version != "" {
		confidence = "high"
	}

	activePorts := uniqueStrings([]string{servicePort, apiPort})
	pidsRaw, _ := markerValue(lines, "PIDS")
	nodePIDs := parsePIDs(pidsRaw)
	xrayPorts := uniqueStrings(append(
		containerXrayPorts(container.Ports, activePorts),
		xrayPortsForPIDs(listenSockets, nodePIDs, servicePort, apiPort)...,
	))
	sortPortsNumeric(xrayPorts)

	return normalizeSnapshot(Snapshot{
		NodeType:     rebeccaType,
		ServiceName:  name,
		InstallMode:  installMode,
		Version:      version,
		HealthStatus: health,
		ActivePorts:  activePorts,
		XrayPorts:    xrayPorts,
		ServicePort:  servicePort,
		APIPort:      apiPort,
		Protocol:     protocol,
		DataDir:      dataDir,
		Confidence:   confidence,
		Evidence:     evidence,
		CollectedAt:  collectedAt,
	}), true
}

func parseRebeccaRelease(raw string) (version, installMode, image string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", ""
	}
	var release struct {
		Tag         string `json:"tag"`
		InstallMode string `json:"install_mode"`
		Image       string `json:"image"`
	}
	if err := json.Unmarshal([]byte(raw), &release); err != nil {
		return "", "", ""
	}
	return normalizeNodeVersion(release.Tag), strings.ToLower(strings.TrimSpace(release.InstallMode)), strings.TrimSpace(release.Image)
}

func isRebeccaNodeImage(image string) bool {
	return strings.Contains(strings.ToLower(image), "rebecca-node")
}

func rebeccaHealth(installMode, serviceState, containerStatus string) string {
	return rebeccaHealthWithCheck(installMode, serviceState, "", "", containerStatus)
}

func rebeccaHealthWithCheck(installMode, serviceState, inspectState, healthCheckState, containerStatus string) string {
	serviceState = strings.ToLower(strings.TrimSpace(serviceState))
	inspectState = strings.ToLower(strings.TrimSpace(inspectState))
	healthCheckState = strings.ToLower(strings.TrimSpace(healthCheckState))
	psLower := strings.ToLower(strings.TrimSpace(containerStatus))
	containerRunning := strings.HasPrefix(psLower, "up")

	if installMode == "docker" {
		if inspectState == "restarting" || strings.Contains(psLower, "restarting") {
			return "restarting"
		}
		if inspectState == "running" || containerRunning {
			if healthCheckState == "unhealthy" || strings.Contains(psLower, "(unhealthy)") {
				return "degraded"
			}
			return "running"
		}
		if inspectState != "" || strings.TrimSpace(containerStatus) != "" {
			return "stopped"
		}
	}

	switch serviceState {
	case "active":
		return "running"
	case "activating":
		return "restarting"
	case "inactive", "failed", "deactivating":
		return "stopped"
	}
	if inspectState == "restarting" || strings.Contains(psLower, "restarting") {
		return "restarting"
	}
	if inspectState == "running" || containerRunning {
		if healthCheckState == "unhealthy" || strings.Contains(psLower, "(unhealthy)") {
			return "degraded"
		}
		return "running"
	}
	return "unknown"
}

// ── Management actions ────────────────────────────────────────────────────────

func (RebeccaProvider) Actions() []Action {
	return []Action{
		{Key: "start", Label: "nodes.action_start", Icon: "play", Timeout: 3 * time.Minute},
		{Key: "stop", Label: "nodes.action_stop", Icon: "square", Timeout: 3 * time.Minute},
		{Key: "restart", Label: "nodes.action_restart", Icon: "rotate-cw", Timeout: 5 * time.Minute},
		{Key: "status", Label: "nodes.action_status", Icon: "activity", Timeout: 2 * time.Minute},
		{Key: "logs", Label: "nodes.action_logs", Icon: "scroll-text", Timeout: 2 * time.Minute},
		{Key: "update", Label: "nodes.action_update", Icon: "arrow-up-circle", Timeout: 20 * time.Minute},
		{Key: "reinstall", Label: "nodes.action_reinstall", Icon: "refresh-cw", Timeout: 5 * time.Minute},
		{Key: "uninstall", Label: "nodes.action_uninstall", Icon: "trash-2", Danger: true, Timeout: 10 * time.Minute},
	}
}

// rebeccaOps maps action keys to rebecca-node CLI operations.  `yes |` keeps
// confirmation prompts (update/uninstall) non-interactive.
var rebeccaOps = map[string]struct {
	op      string
	confirm bool
}{
	"start":     {op: "up"},
	"stop":      {op: "down"},
	"restart":   {op: "restart"},
	"status":    {op: "status"},
	"logs":      {op: "logs --no-follow"},
	"update":    {op: "update", confirm: true},
	"uninstall": {op: "uninstall", confirm: true},
}

// ActionCommand builds `REBECCA_NODE_APP_NAME=<name> rebecca-node <op>`.
//
// The env var is the whole point: upstream resolves the target instance from
// REBECCA_NODE_APP_NAME (or the CLI's own filename). Invoking the bare
// `rebecca-node <op>` on a host with several instances drives whichever one it
// happens to discover — or blocks on an interactive picker when stdin is a
// terminal. Passing the var pins the instance for every operation.
//
// `env` is used rather than a leading VAR=... assignment because the CLI may be
// run through sudo, which would otherwise drop the variable.
func (p RebeccaProvider) ActionCommand(nodeName, actionKey string) (string, time.Duration, error) {
	if err := ValidateNodeName(nodeName); err != nil {
		return "", 0, err
	}
	action, ok := actionByKey(p.Actions(), actionKey)
	if !ok {
		return "", 0, fmt.Errorf("nodes: rebecca: unsupported action %q", actionKey)
	}

	// Reinstall repairs the management CLI rather than driving it (the broken CLI
	// is exactly what it replaces), so it bypasses the `rebecca-node <op>` path.
	if action.Key == "reinstall" {
		return p.reinstallScriptCommand(nodeName), action.Timeout, nil
	}

	spec := rebeccaOps[action.Key]

	invocation := `$SUDO env REBECCA_NODE_APP_NAME=` + nodeName + ` rebecca-node ` + spec.op
	if spec.confirm {
		invocation = `yes | ` + invocation
	} else {
		invocation += ` </dev/null`
	}
	command := `sh -c '` + sudoPreamble +
		`if ! command -v rebecca-node >/dev/null 2>&1; then ` +
		`if command -v ` + nodeName + ` >/dev/null 2>&1; then ln -s "$(command -v ` + nodeName + `)" /usr/local/bin/rebecca-node 2>/dev/null || true; fi; ` +
		`fi; ` +
		`if ! command -v rebecca-node >/dev/null 2>&1; then echo "rebecca-node CLI not found on this server" >&2; exit 86; fi; ` +
		invocation + `'`
	return command, action.Timeout, nil
}

// reinstallScriptCommand repairs the rebecca-node management CLI by downloading
// the binary install script and running its `script-install`, which rewrites
// /usr/local/bin/<name> with the binary-flavored copy. This fixes installs
// whose CLI was left docker-flavored (so `rebecca-node update`/actions aborted
// with the binary/docker mode mismatch). Only the CLI script is reinstalled —
// the node's .env, data, and systemd service are untouched, so no bundle is
// needed. Running the binary script directly means script_install_mode()
// resolves to binary, matching the install, so the mode guard passes.
func (RebeccaProvider) reinstallScriptCommand(nodeName string) string {
	return `sh -c '` + sudoPreamble +
		`REBECCA_URL="` + rebeccaScriptURLStable + `"; ` +
		`if [ -f "/opt/` + nodeName + `/.binary-release.json" ] && $SUDO grep -Eqi "dev" "/opt/` + nodeName + `/.binary-release.json" 2>/dev/null; then REBECCA_URL="` + rebeccaScriptURLDev + `"; fi; ` +
		strings.ReplaceAll(downloadPreamble, "URL", `"$REBECCA_URL"`) +
		`$SUDO env REBECCA_NODE_SCRIPT_FLAVOR=binary bash "$SCRIPT" script-install --name ` + nodeName + ` </dev/null; ` +
		`STATUS=$?; rm -f "$SCRIPT"; ` +
		`if [ "$STATUS" -ne 0 ]; then echo "[rebecca-node script reinstall exited with status $STATUS]" >&2; fi; ` +
		`exit $STATUS'`
}

// ── Installation (stable / dev channels, binary mode) ─────────────────────────
//
// Rebecca's install model is the OPPOSITE of PasarGuard's. PasarGuard generates
// an API key + self-signed cert on the node and the panel reads them back.
// Rebecca does not hand anything back: the USER takes the node install bundle
// from their Rebecca panel and provides it to the installer. So the install
// input is that bundle plus the two ports, and there is no readback step.
//
// We install Rebecca-node in BINARY mode (native systemd service, no Docker) —
// that is the supported footprint and it is what discovery reads (.env,
// .binary-release.json, .install-mode=binary, the rebecca-node systemd unit).
// The binary-flavored script is fetched from the channel's git ref, so stable
// and dev are the same code path with a different URL.
//
// How the script consumes its inputs in binary mode (verified against
// configure_binary_node_env + read_node_certificate_bundle): the cert and key
// files are pre-written to /var/lib/<name>/cert.pem and cert.key before the
// script runs. configure_binary_node_env checks for existing non-empty cert
// files and skips the interactive bundle reader entirely. Ports are then read
// by prompt_node_port_setting, which defaults from the already-present .env and
// only blocks when stdin is a terminal.
//
// Release channel and install mode are passed as native flags (--dev/--stable,
// --binary) so no interactive branch is reachable. The install is invoked with
// stdin NOT a terminal: the script dispatcher redirects `install_command` to
// /dev/tty whenever stdin is not a tty AND /dev/tty is readable, so an SSH
// session WITH a controlling terminal would still leave port prompts waiting.
// Closing stdin on the command therefore makes every prompt fall back to its
// default, which the pre-written .env supplies.

// RebeccaInstallConfig carries the pre-install choices for a Rebecca install.
// Bundle is the node install bundle from the Rebecca panel: the client
// certificate PEM followed by its private key PEM.
type RebeccaInstallConfig struct {
	NodeName    string
	Channel     string
	ServicePort string
	APIPort     string
	Bundle      string
}

// Normalize fills port defaults and validates each field, returning a cleaned
// copy whose Bundle is re-assembled as certificate-then-key (the order the
// script's bundle reader requires).
func (c RebeccaInstallConfig) Normalize() (RebeccaInstallConfig, error) {
	out := RebeccaInstallConfig{
		NodeName:    strings.TrimSpace(c.NodeName),
		Channel:     strings.ToLower(strings.TrimSpace(c.Channel)),
		ServicePort: strings.TrimSpace(c.ServicePort),
		APIPort:     strings.TrimSpace(c.APIPort),
		Bundle:      strings.TrimSpace(c.Bundle),
	}
	if out.NodeName == "" {
		out.NodeName = rebeccaInstallName
	}
	if err := ValidateNodeName(out.NodeName); err != nil {
		return RebeccaInstallConfig{}, fmt.Errorf("nodes: rebecca: %w", err)
	}
	if out.Channel == "" {
		out.Channel = channelStable
	}
	if out.ServicePort == "" {
		out.ServicePort = rebeccaDefaultServicePort
	}
	if out.APIPort == "" {
		out.APIPort = rebeccaDefaultAPIPort
	}
	if !isValidPort(out.ServicePort) {
		return RebeccaInstallConfig{}, fmt.Errorf("nodes: rebecca: invalid service port %q", c.ServicePort)
	}
	if !isValidPort(out.APIPort) {
		return RebeccaInstallConfig{}, fmt.Errorf("nodes: rebecca: invalid API port %q", c.APIPort)
	}
	if out.ServicePort == out.APIPort {
		return RebeccaInstallConfig{}, fmt.Errorf("nodes: rebecca: service and API ports must differ")
	}
	bundle, ok := normalizeRebeccaBundle(out.Bundle)
	if !ok {
		return RebeccaInstallConfig{}, fmt.Errorf("nodes: rebecca: bundle must contain a certificate and a private key")
	}
	out.Bundle = bundle
	if !rebeccaChannelEnabled(out.Channel) {
		return RebeccaInstallConfig{}, fmt.Errorf("nodes: rebecca: channel %q is not available for install", out.Channel)
	}
	return out, nil
}

// rebeccaCertBlockPattern / rebeccaKeyBlockPattern extract the whole PEM blocks
// from a pasted bundle. The key pattern mirrors the script's optional key type,
// accepting RSA / EC / ENCRYPTED keys and bare PKCS#8 PRIVATE KEY blocks.
var (
	rebeccaCertBlockPattern = regexp.MustCompile(`(?s)-----BEGIN CERTIFICATE-----.*?-----END CERTIFICATE-----`)
	rebeccaKeyBlockPattern  = regexp.MustCompile(`(?s)-----BEGIN(?: [^\n-]+)? PRIVATE KEY-----.*?-----END(?: [^\n-]+)? PRIVATE KEY-----`)
)

// normalizeRebeccaBundle pulls the certificate and private-key blocks out of a
// pasted bundle and returns them re-joined as cert-then-key (the order the
// script's reader requires), regardless of how the user pasted them. ok=false
// when either block is missing.
func normalizeRebeccaBundle(s string) (string, bool) {
	cert := rebeccaCertBlockPattern.FindString(s)
	key := rebeccaKeyBlockPattern.FindString(s)
	if cert == "" || key == "" {
		return "", false
	}
	return strings.TrimSpace(cert) + "\n" + strings.TrimSpace(key) + "\n", true
}

// splitRebeccaBundle extracts the certificate and key PEM blocks separately
// from a normalized bundle string. ok=false when either block is missing.
func splitRebeccaBundle(s string) (cert, key string, ok bool) {
	cert = rebeccaCertBlockPattern.FindString(s)
	key = rebeccaKeyBlockPattern.FindString(s)
	if cert == "" || key == "" {
		return "", "", false
	}
	return strings.TrimSpace(cert) + "\n", strings.TrimSpace(key) + "\n", true
}

// normalizeInstallInput validates the raw install form fields for a Rebecca
// install and returns the resolved config plus field-keyed validation errors
// (the values are i18n keys, `nodes.err_*`).
func (RebeccaProvider) normalizeInstallInput(in InstallFormInput) (RebeccaInstallConfig, ValidationErrors) {
	errs := ValidationErrors{}

	cfg := RebeccaInstallConfig{
		NodeName:    strings.TrimSpace(in.NodeName),
		Channel:     strings.ToLower(strings.TrimSpace(in.Channel)),
		ServicePort: strings.TrimSpace(in.ServicePort),
		APIPort:     strings.TrimSpace(in.APIPort),
		Bundle:      strings.TrimSpace(in.Bundle),
	}
	if cfg.Channel == "" {
		cfg.Channel = channelStable
	}
	// A blank name defaults to the primary instance; a non-blank one must be valid.
	if cfg.NodeName != "" {
		if err := ValidateNodeName(cfg.NodeName); err != nil {
			errs["node_name"] = "nodes.err_node_name"
		}
	}
	servicePort := cfg.ServicePort
	if servicePort == "" {
		servicePort = rebeccaDefaultServicePort
	}
	apiPort := cfg.APIPort
	if apiPort == "" {
		apiPort = rebeccaDefaultAPIPort
	}

	if !isValidPort(servicePort) {
		errs["service_port"] = "nodes.err_port_range"
	}
	if !isValidPort(apiPort) {
		errs["api_port"] = "nodes.err_port_range"
	} else if isValidPort(servicePort) && servicePort == apiPort {
		errs["api_port"] = "nodes.err_ports_equal"
	}
	if cfg.Bundle == "" {
		errs["bundle"] = "nodes.err_bundle_required"
	} else if _, ok := normalizeRebeccaBundle(cfg.Bundle); !ok {
		errs["bundle"] = "nodes.err_bundle_pem"
	}
	if !rebeccaChannelEnabled(cfg.Channel) {
		errs["channel"] = "nodes.err_channel_disabled"
	}
	if errs.HasAny() {
		return RebeccaInstallConfig{}, errs
	}

	normalized, err := cfg.Normalize()
	if err != nil {
		errs["bundle"] = "nodes.err_bundle_pem"
		if strings.Contains(err.Error(), "channel") {
			delete(errs, "bundle")
			errs["channel"] = "nodes.err_channel_disabled"
		} else if strings.Contains(err.Error(), "service port") {
			delete(errs, "bundle")
			errs["service_port"] = "nodes.err_port_range"
		} else if strings.Contains(err.Error(), "API port") {
			delete(errs, "bundle")
			errs["api_port"] = "nodes.err_port_range"
		} else if strings.Contains(err.Error(), "must differ") {
			delete(errs, "bundle")
			errs["api_port"] = "nodes.err_ports_equal"
		}
		return RebeccaInstallConfig{}, errs
	}
	return normalized, errs
}

func wrapBase64Lines(encoded string, width int) string {
	if width <= 0 || len(encoded) <= width {
		return encoded
	}
	var b strings.Builder
	b.Grow(len(encoded) + len(encoded)/width + 2)
	for i := 0; i < len(encoded); i += width {
		if i > 0 {
			b.WriteByte('\n')
		}
		end := i + width
		if end > len(encoded) {
			end = len(encoded)
		}
		b.WriteString(encoded[i:end])
	}
	return b.String()
}

// InstallCommand builds the Rebecca binary-mode installer shell command.
// Secrets (.env, cert.pem, cert.key) are NOT placed in the command string —
// they travel via InstallStage.Input over a non-echoing PTY (`AllocatePTY: true`)
// and are written directly under `umask 077`.
func (p RebeccaProvider) InstallCommand(cfg RebeccaInstallConfig) (string, error) {
	cmd, _, err := p.installSpec(cfg)
	return cmd, err
}

func (RebeccaProvider) installSpec(cfg RebeccaInstallConfig) (string, string, error) {
	normalized, err := cfg.Normalize()
	if err != nil {
		return "", "", err
	}

	certPEM, keyPEM, ok := splitRebeccaBundle(normalized.Bundle)
	if !ok {
		return "", "", fmt.Errorf("nodes: rebecca: bundle must contain a certificate and a private key")
	}

	scriptURL := rebeccaScriptURL(normalized.Channel)
	dataDir := "/var/lib/" + normalized.NodeName
	appDir := "/opt/" + normalized.NodeName

	certB64 := wrapBase64Lines(base64.StdEncoding.EncodeToString([]byte(certPEM)), 76)
	keyB64 := wrapBase64Lines(base64.StdEncoding.EncodeToString([]byte(keyPEM)), 76)

	envContent := fmt.Sprintf(
		"SERVICE_PORT = \"%s\"\nXRAY_API_PORT = \"%s\"\nREBECCA_DATA_DIR = \"%s\"\n"+
			"SSL_CLIENT_CERT_FILE = \"%s/cert.pem\"\nSSL_CERT_FILE = \"%s/cert.pem\"\n"+
			"SSL_KEY_FILE = \"%s/cert.key\"\nXRAY_EXECUTABLE_PATH = \"%s/xray-core/xray\"\n"+
			"XRAY_ASSETS_PATH = \"%s/xray-core\"\nSERVICE_PROTOCOL = \"%s\"\n",
		normalized.ServicePort, normalized.APIPort,
		dataDir, dataDir, dataDir, dataDir, dataDir, dataDir,
		rebeccaDefaultProtocol)
	envB64 := wrapBase64Lines(base64.StdEncoding.EncodeToString([]byte(envContent)), 76)

	stdinPayload := envB64 + "\n==ENDENV==\n" +
		certB64 + "\n==ENDCERT==\n" +
		keyB64 + "\n==ENDKEY==\n"

	channelFlag := ` --version latest`
	if normalized.Channel == channelDev {
		channelFlag = ` --dev`
	}

	// Secret files are transferred through framed stdin over the non-echoing
	// PTY (`AllocatePTY: true`, ECHO=0) and written under `umask 077` inside a
	// `0700` data directory — never placed in shell argv (`ps aux`) and never
	// exposed world-readable before chmod.
	command := `sh -c '` + sudoPreamble +
		stageMarker(stageInitializing) +
		`$SUDO mkdir -p ` + appDir + ` ` + dataDir + ` && $SUDO chmod 700 ` + dataDir + ` || exit 1; ` +
		`NODEXIA_UMASK=$(umask); umask 077; _CR=$(printf "\r"); ` +
		`_ENV_T=$(mktemp "${TMPDIR:-/tmp}/nodexia-e.XXXXXX") || exit 1; ` +
		`_CERT_T=$(mktemp "${TMPDIR:-/tmp}/nodexia-c.XXXXXX") || { rm -f "$_ENV_T"; exit 1; }; ` +
		`_KEY_T=$(mktemp "${TMPDIR:-/tmp}/nodexia-k.XXXXXX") || { rm -f "$_ENV_T" "$_CERT_T"; exit 1; }; ` +
		`while IFS= read -r _l; do _l=${_l%$_CR}; [ "$_l" = "==ENDENV==" ] && break; printf "%s\n" "$_l" >> "$_ENV_T"; done; ` +
		`while IFS= read -r _l; do _l=${_l%$_CR}; [ "$_l" = "==ENDCERT==" ] && break; printf "%s\n" "$_l" >> "$_CERT_T"; done; ` +
		`while IFS= read -r _l; do _l=${_l%$_CR}; [ "$_l" = "==ENDKEY==" ] && break; printf "%s\n" "$_l" >> "$_KEY_T"; done; ` +
		`base64 -d < "$_ENV_T" | $SUDO sh -c "umask 077; cat > ` + appDir + `/.env" || { rm -f "$_ENV_T" "$_CERT_T" "$_KEY_T"; exit 1; }; ` +
		`base64 -d < "$_CERT_T" | $SUDO sh -c "umask 022; cat > ` + dataDir + `/cert.pem" || { rm -f "$_ENV_T" "$_CERT_T" "$_KEY_T"; exit 1; }; ` +
		`base64 -d < "$_KEY_T" | $SUDO sh -c "umask 077; cat > ` + dataDir + `/cert.key" || { rm -f "$_ENV_T" "$_CERT_T" "$_KEY_T"; exit 1; }; ` +
		`rm -f "$_ENV_T" "$_CERT_T" "$_KEY_T"; ` +
		`umask "$NODEXIA_UMASK"; ` +
		stageMarker(stageDownloading) +
		strings.ReplaceAll(downloadPreamble, "URL", scriptURL) +
		`$SUDO sed -i "s|</dev/tty||g; s|\\[ -r /dev/tty \\]|false|g" "$SCRIPT"; ` +
		stageMarker(stageDeploying) +
		`TMO=""; if command -v timeout >/dev/null 2>&1; then TMO="timeout ` + rebeccaInstallScriptTimeout + `"; fi; ` +
		`printf "y\n" | $TMO $SUDO env REBECCA_NODE_SCRIPT_FLAVOR=binary bash "$SCRIPT" install --name ` + normalized.NodeName +
		` --binary` + channelFlag + `; ` +
		`STATUS=$?; rm -f "$SCRIPT"; ` +
		`if [ "$STATUS" -ne 0 ]; then echo "[rebecca-node install script exited with status $STATUS]" >&2; fi; ` +
		`exit $STATUS'`
	return command, stdinPayload, nil
}

// BuildInstallPlan assembles the Rebecca install procedure: one streamed stage
// using a non-echoing PTY for managed secret transfer (`Input`) before invoking
// the official binary script.
func (p RebeccaProvider) BuildInstallPlan(in InstallFormInput) (InstallPlan, ValidationErrors) {
	cfg, errs := p.normalizeInstallInput(in)
	if errs.HasAny() {
		return InstallPlan{}, errs
	}
	installCmd, stdinInput, err := p.installSpec(cfg)
	if err != nil {
		errs["bundle"] = "nodes.err_bundle_pem"
		return InstallPlan{}, errs
	}
	plan := InstallPlan{
		Stages: []InstallStage{
			{
				Key:         stageInitializing,
				StartLog:    fmt.Sprintf("\n[deploying Rebecca node %q — channel %s, binary mode, service port %s]\n", cfg.NodeName, cfg.Channel, cfg.ServicePort),
				Command:     installCmd,
				Input:       stdinInput,
				AllocatePTY: true,
				Timeout:     installCommandTimeout,
			},
		},
	}
	return plan, errs
}
