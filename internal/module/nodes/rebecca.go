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
// Management goes through the official `rebecca-node` CLI. Nodexia can install
// the dev/beta channel from the panel (binary mode); the stable channel is
// declared but not yet enabled. See the Installation section below.
type RebeccaProvider struct{}

const (
	rebeccaType = "rebecca-node"
	// Defaults written by the official rebecca-node install script.
	rebeccaDefaultServicePort = "62050"
	rebeccaDefaultAPIPort     = "62051"
	rebeccaDefaultProtocol    = "rest"

	// rebeccaInstallName is the default instance name (used when the install form
	// leaves the name blank). A custom --name installs under /opt/<name> so a host
	// can run several Rebecca nodes side by side; discovery scans /opt for them.
	rebeccaInstallName = "rebecca-node"

	// rebeccaDevScriptURL is the dev/beta install script. We deliberately use the
	// BINARY-flavored script (rebecca-node-binary.sh), not the docker one: the
	// script installs the `rebecca-node` management CLI as a copy of itself, so
	// running the docker script would leave a docker-flavored CLI on a
	// binary-mode install — every later `rebecca-node update`/action then aborts
	// with "installation is in binary mode, but rebecca-node is the docker
	// script". The binary script installs a binary-flavored CLI that matches.
	// The stable script lives on a different ref; wiring stable on later means
	// adding its URL and flipping channelStable's Enabled flag (see InstallChannels).
	rebeccaDevScriptURL = "https://raw.githubusercontent.com/rebeccapanel/Rebecca/dev/scripts/rebecca/rebecca-node-binary.sh"

	// rebeccaInstallScriptTimeout bounds the install script remotely. The
	// docker dev install runs `compose up -d` (detached, no log tail), so a
	// clean run exits 0 well within this; the bound just backstops a hung pull.
	rebeccaInstallScriptTimeout = "600"
)

// Install channel keys. The "channel" concept (stable vs dev/beta) is modeled
// as data so it can apply to other providers later, and so enabling a channel
// is a one-line flip rather than a rewrite.
const (
	channelStable = "stable"
	channelDev    = "dev"
)

// InstallChannel is one release channel a provider can install from the panel.
// Enabled=false renders as "coming soon" in the UI and is rejected server-side,
// so turning a channel on later is a one-line flip here plus its plan branch.
type InstallChannel struct {
	Key     string
	Enabled bool
}

func (RebeccaProvider) Type() string        { return rebeccaType }
func (RebeccaProvider) DisplayName() string { return "Rebecca" }

// SupportsInstall is true now that the dev/beta channel installs from the panel.
// The stable channel is declared but disabled (see InstallChannels).
func (RebeccaProvider) SupportsInstall() bool { return true }

// InstallChannels lists Rebecca's release channels. Only dev (beta) installs
// today; stable is present-but-disabled ("coming soon"). To enable stable:
// flip Enabled here, add its script URL, and add a stable branch to
// BuildInstallPlan — no other wiring changes.
func (RebeccaProvider) InstallChannels() []InstallChannel {
	return []InstallChannel{
		{Key: channelDev, Enabled: true},
		{Key: channelStable, Enabled: false},
	}
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

	invocation := `$SUDO rebecca-node ` + spec.op + ` </dev/null`
	if spec.confirm {
		invocation = `yes | $SUDO rebecca-node ` + spec.op
	}
	command := `sh -c '` + sudoPreamble +
		`if ! command -v rebecca-node >/dev/null 2>&1; then echo "rebecca-node CLI not found on this server" >&2; exit 86; fi; ` +
		invocation + `'`
	return command, action.Timeout, nil
}

// reinstallScriptCommand repairs the rebecca-node management CLI by downloading
// the binary install script and running its `script-install`, which rewrites
// /usr/local/bin/rebecca-node with the binary-flavored copy. This fixes installs
// whose CLI was left docker-flavored (so `rebecca-node update`/actions aborted
// with the binary/docker mode mismatch). Only the CLI script is reinstalled —
// the node's .env, data, and systemd service are untouched, so no bundle is
// needed. Running the binary script directly means script_install_mode() resolves
// to binary, matching the install, so the mode guard passes.
func (RebeccaProvider) reinstallScriptCommand(nodeName string) string {
	return `sh -c '` + sudoPreamble +
		`SCRIPT="$(mktemp /tmp/nodexia-rebecca-node.XXXXXX)" || exit 1; ` +
		`if command -v curl >/dev/null 2>&1; then curl -fsSL ` + rebeccaDevScriptURL + ` -o "$SCRIPT" || { echo "download failed" >&2; rm -f "$SCRIPT"; exit 85; }; ` +
		`elif command -v wget >/dev/null 2>&1; then wget -qO "$SCRIPT" ` + rebeccaDevScriptURL + ` || { echo "download failed" >&2; rm -f "$SCRIPT"; exit 85; }; ` +
		`else echo "curl or wget is required to reinstall" >&2; rm -f "$SCRIPT"; exit 85; fi; ` +
		`$SUDO env REBECCA_NODE_SCRIPT_FLAVOR=binary bash "$SCRIPT" script-install --name ` + nodeName + ` </dev/null; ` +
		`STATUS=$?; rm -f "$SCRIPT"; ` +
		`if [ "$STATUS" -ne 0 ]; then echo "[rebecca-node script reinstall exited with status $STATUS]" >&2; fi; ` +
		`exit $STATUS'`
}

// ── Installation (dev/beta channel) ────────────────────────────────────────────
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
// We run the binary-flavored script (rebecca-node-binary.sh, see
// rebeccaDevScriptURL) so the `rebecca-node` CLI it installs is also binary
// flavored and later update/actions work. REBECCA_NODE_SCRIPT_FLAVOR=binary is
// passed via `env` (surviving sudo) to make the binary mode explicit.
//
// How rebecca-node.sh consumes its inputs in binary mode (verified against the
// script's read_node_certificate_bundle + configure_binary_node_env): it reads
// stdin in this exact order —
//  1. the node install BUNDLE — the client CERTIFICATE block followed by its
//     PRIVATE KEY block. The reader appends lines until it sees the
//     "-----END <type> PRIVATE KEY-----" line with the certificate already
//     present, so the certificate MUST come before the key;
//  2. the SERVICE_PORT (the protocol is auto-set to REST, no prompt);
//  3. the XRAY_API_PORT (must differ from SERVICE_PORT).
// It then writes /opt/rebecca-node/.env + the binary release metadata and
// enables the systemd unit (`systemctl enable --now`, which returns at once).
//
// The bundle is multi-line PEM, so a bare interactive `read` can't carry it. We
// deliver it robustly by base64-encoding the (re-ordered) bundle on our side and
// decoding it into a temp stdin file on the remote, then appending the two
// ports. base64 has no quotes/newlines/metacharacters, so it is safe inside the
// outer `sh -c '...'` (no single quote ever appears — the
// TestGeneratedShellSyntax guard) and immune to set -e / non-tty / SSH issues.
// The private key is sensitive: it only ever exists as in-memory base64, is
// never persisted, and the script echoes "bundle saved", never its contents.

// RebeccaInstallConfig carries the pre-install choices for a Rebecca dev install.
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
// script's bundle reader requires). Field-keyed validation lives in
// normalizeInstallInput; this guards the command builder so a malformed config
// can never reach the shell.
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
		out.Channel = channelDev
	}
	if out.ServicePort == "" {
		out.ServicePort = rebeccaDefaultServicePort
	}
	if out.APIPort == "" {
		out.APIPort = rebeccaDefaultAPIPort
	}
	if validPort(out.ServicePort) == "" {
		return RebeccaInstallConfig{}, fmt.Errorf("nodes: rebecca: invalid service port %q", c.ServicePort)
	}
	if validPort(out.APIPort) == "" {
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
// from a pasted bundle. The key pattern mirrors the script's terminator
// (`-----END .+PRIVATE KEY-----`): the key type must carry a prefix word
// (RSA / EC / ENCRYPTED …), exactly what the installer accepts.
var (
	rebeccaCertBlockPattern = regexp.MustCompile(`(?s)-----BEGIN CERTIFICATE-----.*?-----END CERTIFICATE-----`)
	rebeccaKeyBlockPattern  = regexp.MustCompile(`(?s)-----BEGIN [^\n-]+PRIVATE KEY-----.*?-----END [^\n-]+PRIVATE KEY-----`)
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

// normalizeInstallInput validates the raw install form fields for a Rebecca
// install and returns the resolved config plus field-keyed validation errors
// (the values are i18n keys the handler translates). Empty errors means valid.
func (RebeccaProvider) normalizeInstallInput(in installFormInput) (RebeccaInstallConfig, ValidationErrors) {
	errs := ValidationErrors{}

	cfg := RebeccaInstallConfig{
		NodeName:    strings.TrimSpace(in.NodeName),
		Channel:     strings.ToLower(strings.TrimSpace(in.Channel)),
		ServicePort: strings.TrimSpace(in.ServicePort),
		APIPort:     strings.TrimSpace(in.APIPort),
		Bundle:      strings.TrimSpace(in.Bundle),
	}
	if cfg.Channel == "" {
		cfg.Channel = channelDev
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

	if validPort(servicePort) == "" {
		errs["service_port"] = "nodes.err_port_range"
	}
	if validPort(apiPort) == "" {
		errs["api_port"] = "nodes.err_port_range"
	} else if validPort(servicePort) != "" && servicePort == apiPort {
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

// InstallCommand downloads and runs the official rebecca-node dev install
// script in BINARY mode, feeding the install bundle (base64-decoded into a temp
// stdin file) and the two ports the way the binary install path reads them.
func (RebeccaProvider) InstallCommand(cfg RebeccaInstallConfig) (string, error) {
	normalized, err := cfg.Normalize()
	if err != nil {
		return "", err
	}
	scriptURL := rebeccaDevScriptURL // only dev wired today; stable adds its URL here

	// The bundle (cert + private key) never appears literally in the command —
	// only as base64, which carries no quotes/newlines and so is safe inside
	// sh -c '...'. The script's bundle reader stops at the END PRIVATE KEY line,
	// so the ports follow immediately (no terminating blank line needed).
	bundleB64 := base64.StdEncoding.EncodeToString([]byte(normalized.Bundle))

	command := `sh -c '` + sudoPreamble +
		`SCRIPT="$(mktemp /tmp/nodexia-rebecca-node.XXXXXX)" || exit 1; ` +
		`if command -v curl >/dev/null 2>&1; then curl -fsSL ` + scriptURL + ` -o "$SCRIPT" || { echo "download failed" >&2; rm -f "$SCRIPT"; exit 85; }; ` +
		`elif command -v wget >/dev/null 2>&1; then wget -qO "$SCRIPT" ` + scriptURL + ` || { echo "download failed" >&2; rm -f "$SCRIPT"; exit 85; }; ` +
		`else echo "curl or wget is required to install" >&2; rm -f "$SCRIPT"; exit 85; fi; ` +
		// Build the stdin the binary install expects: the bundle (cert+key),
		// then SERVICE_PORT and XRAY_API_PORT.
		`IN="$(mktemp /tmp/nodexia-rebecca-in.XXXXXX)" || { rm -f "$SCRIPT"; exit 1; }; ` +
		`printf "%s" "` + bundleB64 + `" | base64 -d > "$IN" || { echo "bundle decode failed" >&2; rm -f "$SCRIPT" "$IN"; exit 1; }; ` +
		`printf "` + normalized.ServicePort + `\n` + normalized.APIPort + `\n" >> "$IN"; ` +
		`TMO=""; if command -v timeout >/dev/null 2>&1; then TMO="timeout ` + rebeccaInstallScriptTimeout + `"; fi; ` +
		// Run the binary-flavored script in binary mode (REBECCA_NODE_SCRIPT_FLAVOR
		// carries through sudo). --name pins the instance to /opt/<name> so several
		// Rebecca nodes can coexist; the COMMAND (install) must be parsed before
		// --name, so order matters. NodeName is validated in Normalize.
		`$TMO $SUDO env REBECCA_NODE_SCRIPT_FLAVOR=binary bash "$SCRIPT" install --name ` + normalized.NodeName + ` --binary --dev <"$IN"; ` +
		`STATUS=$?; rm -f "$SCRIPT" "$IN"; ` +
		`if [ "$STATUS" -ne 0 ]; then echo "[rebecca-node install script exited with status $STATUS]" >&2; fi; ` +
		`exit $STATUS'`
	return command, nil
}

// BuildInstallPlan assembles the Rebecca dev install procedure: a single
// streamed step that runs the official script. There is no configure step and
// no readback — the bundle came from the user, not the node.
func (p RebeccaProvider) BuildInstallPlan(in installFormInput) (InstallPlan, ValidationErrors) {
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
		Steps: []InstallStep{
			{Command: installCmd, Timeout: installCommandTimeout},
		},
	}
	return plan, errs
}
