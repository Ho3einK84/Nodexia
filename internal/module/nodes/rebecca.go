package nodes

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
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
		`for dir in /opt/*/; do ` +
		`base="${dir%/}"; ` +
		`name="${base##*/}"; ` +
		// Identify a Rebecca NODE specifically — NOT the Rebecca panel/server, which
		// lives under /opt too and writes the SAME marker files (.install-mode,
		// .binary-release.json), so the old marker-only check surfaced the panel as a
		// node. A node is told apart by its image: binary nodes tag
		// .binary-release.json image "rebecca-node (binary)"; docker nodes run the
		// rebeccapanel/rebecca-node compose image. The panel tags "rebecca-server
		// (binary)" / runs rebeccapanel/rebecca, so it never matches and is skipped.
		// (PasarGuard does the same via isPasarguardNodeImage.)
		`is_node=0; ` +
		`if [ -f "$base/.binary-release.json" ] && $SUDO grep -Eqi "\"image\"[^,]*rebecca-node" "$base/.binary-release.json" 2>/dev/null; then is_node=1; fi; ` +
		`if [ "$is_node" -eq 0 ]; then for cf in "$base/docker-compose.yml" "$base/docker-compose.yaml" "$base/compose.yml" "$base/compose.yaml"; do [ -f "$cf" ] && $SUDO grep -Eqi "image:[^#]*rebecca-node" "$cf" 2>/dev/null && { is_node=1; break; }; done; fi; ` +
		`[ "$is_node" -eq 1 ] || continue; ` +
		`printf "=REBECCANODE=%s=\n" "$name"; ` +
		`printf "=ENVSTART=\n"; $SUDO cat "$base/.env" 2>/dev/null || true; printf "\n=ENVEND=\n"; ` +
		`printf "=RELEASESTART=\n"; $SUDO cat "$base/.binary-release.json" 2>/dev/null || true; printf "\n=RELEASEEND=\n"; ` +
		`printf "=MODE=%s=\n" "$($SUDO cat "$base/.install-mode" 2>/dev/null)"; ` +
		`printf "=SERVICE=%s=\n" "$(systemctl is-active "$name" 2>/dev/null)"; ` +
		// PIDs owned by this instance's systemd service (the node agent and its
		// xray child share the unit cgroup), so xray ports can be attributed to it
		// rather than to every listener on the host.
		`cg="$(systemctl show -p ControlGroup --value "$name" 2>/dev/null || true)"; ` +
		`PIDS=""; if [ -n "$cg" ] && [ -r "/sys/fs/cgroup$cg/cgroup.procs" ]; then PIDS="$($SUDO cat "/sys/fs/cgroup$cg/cgroup.procs" 2>/dev/null | tr "\n" " " || true)"; fi; ` +
		`printf "=PIDS=%s=\n" "$PIDS"; ` +
		`if [ "$HAVE_DOCKER" -eq 1 ]; then ` +
		`printf "=CONTAINER=%s=\n" "$($SUDO docker ps -a --filter "name=^${name}$" --format "{{.Status}}" 2>/dev/null | head -n 1)"; ` +
		`fi; ` +
		`printf "=REBECCANODEEND=\n"; ` +
		`done; ` +
		// Host listening sockets with owning PIDs (once), matched per instance
		// against the cgroup PIDs above to attribute xray ports precisely.
		listenProbeCommand +
		`true'`
}

func (RebeccaProvider) ParseDiscovery(output string, collectedAt time.Time) []Snapshot {
	lines := strings.Split(output, "\n")

	// Host listening sockets (with owning PIDs), shared by every instance; each
	// instance keeps only the ports owned by its own PIDs.
	listenLines, _ := markerSection(lines, "=LISTENP=", "=LISTENPEND=")
	listenSockets := parseListenSockets(listenLines)

	var snapshots []Snapshot
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
				if snap, ok := parseRebeccaInstance(name, block, listenSockets, collectedAt); ok {
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

	return snapshots
}

// parseRebeccaInstance builds one Snapshot from a single "=REBECCANODE=" block.
// name is the /opt/<name> directory (and systemd/container name) the instance
// lives under.
//
// It returns ok=false when the block belongs to the Rebecca PANEL/server rather
// than a node (they share the /opt layout and marker files): the shell gate
// already excludes the panel, and this is the matching belt-and-suspenders check
// on the parsed metadata so a panel can never slip through as a node.
func parseRebeccaInstance(name string, lines []string, listenSockets []listenSocket, collectedAt time.Time) (Snapshot, bool) {
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

	// Drop the Rebecca panel/server: it has a .binary-release.json image, but one
	// that names the server, not a node. An empty image (e.g. a docker-mode node
	// without binary metadata) is left to the shell gate and kept here.
	if image != "" && !isRebeccaNodeImage(image) {
		return Snapshot{}, false
	}

	installMode, _ := markerValue(lines, "MODE")
	installMode = strings.ToLower(strings.TrimSpace(installMode))
	if installMode == "" {
		installMode = installModeFromRelease
	}
	if installMode == "" {
		installMode = "binary"
	}

	serviceState, _ := markerValue(lines, "SERVICE")
	containerStatus, _ := markerValue(lines, "CONTAINER")
	health := rebeccaHealth(installMode, serviceState, containerStatus)

	appDir := "/opt/" + name
	evidence := []string{"Install directory: " + appDir}
	if len(env) > 0 {
		evidence = append(evidence, fmt.Sprintf("Config: %s/.env (service port %s, protocol %s)", appDir, servicePort, protocol))
	}
	if version != "" {
		evidence = append(evidence, "Version from .binary-release.json: "+version)
	}
	if serviceState != "" {
		evidence = append(evidence, "systemd "+name+": "+serviceState)
	}
	if containerStatus != "" {
		evidence = append(evidence, "Docker container "+name+": "+containerStatus)
	}

	confidence := "medium"
	if len(env) > 0 || version != "" {
		confidence = "high"
	}

	// Xray inbounds = ports owned by this instance's PIDs, minus its own
	// service/api management ports.
	pidsRaw, _ := markerValue(lines, "PIDS")
	nodePIDs := parsePIDs(pidsRaw)
	xrayPorts := xrayPortsForPIDs(listenSockets, nodePIDs, servicePort, apiPort)

	return normalizeSnapshot(Snapshot{
		NodeType:     rebeccaType,
		ServiceName:  name,
		InstallMode:  installMode,
		Version:      version,
		HealthStatus: health,
		ActivePorts:  uniqueStrings([]string{servicePort, apiPort}),
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

// parseRebeccaRelease extracts the version tag, install mode, and image label from
// .binary-release.json (fields "tag", "install_mode", "image"). The image label
// distinguishes a node ("rebecca-node (binary)") from the panel/server
// ("rebecca-server (binary)").
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
	return strings.TrimSpace(release.Tag), strings.ToLower(strings.TrimSpace(release.InstallMode)), strings.TrimSpace(release.Image)
}

// isRebeccaNodeImage reports whether a .binary-release.json / compose image label
// belongs to a Rebecca NODE rather than the panel/server. The node tags its image
// "rebecca-node (binary)" (docker: rebeccapanel/rebecca-node); the panel tags
// "rebecca-server (binary)" (docker: rebeccapanel/rebecca). Mirrors PasarGuard's
// isPasarguardNodeImage so the panel is never surfaced as a node.
func isRebeccaNodeImage(image string) bool {
	return strings.Contains(strings.ToLower(image), "rebecca-node")
}

func rebeccaHealth(installMode, serviceState, containerStatus string) string {
	serviceState = strings.ToLower(strings.TrimSpace(serviceState))
	containerRunning := strings.HasPrefix(strings.ToLower(strings.TrimSpace(containerStatus)), "up")

	if installMode == "docker" {
		switch {
		case containerRunning:
			return "running"
		case strings.TrimSpace(containerStatus) != "":
			return "stopped"
		}
	}

	switch serviceState {
	case "active":
		return "running"
	case "inactive", "failed", "deactivating":
		return "stopped"
	}
	if containerRunning {
		return "running"
	}
	return "unknown"
}

// ── Management actions ────────────────────────────────────────────────────────

func (RebeccaProvider) Actions() []Action {
	return []Action{
		{Key: "start", Label: "Start", Icon: "play", Timeout: 3 * time.Minute},
		{Key: "stop", Label: "Stop", Icon: "square", Timeout: 3 * time.Minute},
		{Key: "restart", Label: "Restart", Icon: "rotate-cw", Timeout: 5 * time.Minute},
		{Key: "status", Label: "Status", Icon: "activity", Timeout: 2 * time.Minute},
		{Key: "logs", Label: "Logs", Icon: "scroll-text", Timeout: 2 * time.Minute},
		{Key: "update", Label: "Update", Icon: "arrow-up-circle", Timeout: 20 * time.Minute},
		{Key: "reinstall", Label: "Reinstall CLI", Icon: "refresh-cw", Timeout: 5 * time.Minute},
		{Key: "uninstall", Label: "Uninstall", Icon: "trash-2", Danger: true, Timeout: 10 * time.Minute},
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

	invocation := `$SUDO env REBECCA_NODE_APP_NAME=` + nodeName + ` rebecca-node ` + spec.op + ` </dev/null`
	if spec.confirm {
		invocation = `yes | ` + invocation
	}
	command := `sh -c '` + sudoPreamble +
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
		strings.ReplaceAll(downloadPreamble, "URL", rebeccaScriptURLDev) +
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
		return RebeccaInstallConfig{}, errs
	}
	return normalized, errs
}

// InstallCommand downloads and runs the official rebecca-node install script in
// BINARY mode for the requested channel.
//
// Pre-writing strategy: the upstream script has several prompts that read from
// stdin. Rather than patching the script with sed (which breaks whenever
// upstream renames a function or reflows a prompt), we pre-create every file
// the prompts would otherwise have to collect:
//
//  1. /opt/<name>/.env — ports, cert paths, protocol. prompt_node_port_setting
//     reads a default from here via get_env_value, so the prompt resolves itself.
//  2. /var/lib/<name>/cert.pem and cert.key — the certificate and private key.
//     configure_binary_node_env skips read_node_certificate_bundle when both
//     files already exist and are non-empty.
//
// With those in place, every remaining prompt has a working default, so closing
// stdin (`</dev/null`) is enough for a fully non-interactive install.
func (RebeccaProvider) InstallCommand(cfg RebeccaInstallConfig) (string, error) {
	normalized, err := cfg.Normalize()
	if err != nil {
		return "", err
	}

	certPEM, keyPEM, ok := splitRebeccaBundle(normalized.Bundle)
	if !ok {
		return "", fmt.Errorf("nodes: rebecca: bundle must contain a certificate and a private key")
	}

	scriptURL := rebeccaScriptURL(normalized.Channel)
	dataDir := "/var/lib/" + normalized.NodeName
	appDir := "/opt/" + normalized.NodeName

	// Base64-encode the PEM files so they survive the outer sh -c '...' wrapper
	// without any quoting concerns.
	certB64 := base64.StdEncoding.EncodeToString([]byte(certPEM))
	keyB64 := base64.StdEncoding.EncodeToString([]byte(keyPEM))

	// Pre-write .env in the script's own set_env_value format (KEY = "VALUE") so
	// get_env_value finds a default for every port prompt.
	envContent := fmt.Sprintf(
		"SERVICE_PORT = \"%s\"\nXRAY_API_PORT = \"%s\"\nREBECCA_DATA_DIR = \"%s\"\n"+
			"SSL_CLIENT_CERT_FILE = \"%s/cert.pem\"\nSSL_CERT_FILE = \"%s/cert.pem\"\n"+
			"SSL_KEY_FILE = \"%s/cert.key\"\nXRAY_EXECUTABLE_PATH = \"%s/xray-core/xray\"\n"+
			"XRAY_ASSETS_PATH = \"%s/xray-core\"\nSERVICE_PROTOCOL = \"%s\"\n",
		normalized.ServicePort, normalized.APIPort,
		dataDir, dataDir, dataDir, dataDir, dataDir, dataDir,
		rebeccaDefaultProtocol)
	envB64 := base64.StdEncoding.EncodeToString([]byte(envContent))

	channelFlag := ` --version latest`
	if normalized.Channel == channelDev {
		channelFlag = ` --dev`
	}

	command := `sh -c '` + sudoPreamble +
		stageMarker(stageInitializing) +
		// Pre-create the directories and the three config files the script would
		// otherwise prompt for.
		`$SUDO mkdir -p ` + appDir + ` ` + dataDir + `; ` +
		`echo ` + envB64 + ` | base64 -d | $SUDO tee ` + appDir + `/.env >/dev/null; ` +
		`echo ` + certB64 + ` | base64 -d | $SUDO tee ` + dataDir + `/cert.pem >/dev/null; ` +
		`echo ` + keyB64 + ` | base64 -d | $SUDO tee ` + dataDir + `/cert.key >/dev/null; ` +
		`$SUDO chmod 600 ` + dataDir + `/cert.key; ` +
		stageMarker(stageDownloading) +
		strings.ReplaceAll(downloadPreamble, "URL", scriptURL) +
		stageMarker(stageDeploying) +
		`TMO=""; if command -v timeout >/dev/null 2>&1; then TMO="timeout ` + rebeccaInstallScriptTimeout + `"; fi; ` +
		// Stdin provides 'y' to confirm the directory override prompt (since appDir was pre-created
		// above to supply .env without interactive prompts), and hits EOF for subsequent port prompts
		// so they cleanly fall back to the pre-written .env values.
		`printf "y\n" | $TMO $SUDO env REBECCA_NODE_SCRIPT_FLAVOR=binary bash "$SCRIPT" install --name ` + normalized.NodeName +
		` --binary` + channelFlag + `; ` +
		`STATUS=$?; rm -f "$SCRIPT"; ` +
		`if [ "$STATUS" -ne 0 ]; then echo "[rebecca-node install script exited with status $STATUS]" >&2; fi; ` +
		`exit $STATUS'`
	return command, nil
}

// BuildInstallPlan assembles the Rebecca install procedure: one streamed stage
// that runs the official script. There is no configure step, and no readback —
// the bundle came from the user, not the node.
func (p RebeccaProvider) BuildInstallPlan(in InstallFormInput) (InstallPlan, ValidationErrors) {
	cfg, errs := p.normalizeInstallInput(in)
	if errs.HasAny() {
		return InstallPlan{}, errs
	}
	installCmd, err := p.InstallCommand(cfg)
	if err != nil {
		errs["bundle"] = "nodes.err_bundle_pem"
		return InstallPlan{}, errs
	}
	plan := InstallPlan{
		Stages: []InstallStage{
			{
				Key:      stageInitializing,
				StartLog: fmt.Sprintf("\n[deploying Rebecca node %q — channel %s, binary mode, service port %s]\n", cfg.NodeName, cfg.Channel, cfg.ServicePort),
				Command:  installCmd,
				Timeout:  installCommandTimeout,
			},
		},
	}
	return plan, errs
}
