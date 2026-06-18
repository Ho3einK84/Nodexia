package nodes

import (
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"time"
)

// RebeccaProvider drives Rebecca nodes (https://github.com/rebeccapanel/Rebecca-node).
//
// Rebecca installs are detected and managed only — Nodexia never installs
// them.  A single instance lives under /opt/rebecca-node (configuration in
// .env, version metadata in .binary-release.json) with data under
// /var/lib/rebecca-node.  Management goes through the official `rebecca-node`
// CLI.
type RebeccaProvider struct{}

const (
	rebeccaType    = "rebecca-node"
	rebeccaAppDir  = "/opt/rebecca-node"
	rebeccaDataDir = "/var/lib/rebecca-node"
	// Defaults written by the official rebecca-node install script.
	rebeccaDefaultServicePort = "62050"
	rebeccaDefaultAPIPort     = "62051"
	rebeccaDefaultProtocol    = "rest"

	// rebeccaInstallName is the fixed instance name we install under. Rebecca
	// discovery is keyed on /opt/rebecca-node and the rebecca-node CLI, so a
	// host hosts a single instance — we pass --name to pin it regardless of how
	// the script derives its default app name.
	rebeccaInstallName = "rebecca-node"

	// rebeccaDevScriptURL is the dev/beta install script. The stable script
	// lives on a different ref; wiring stable on later means adding its URL and
	// flipping channelStable's Enabled flag (see InstallChannels).
	rebeccaDevScriptURL = "https://raw.githubusercontent.com/rebeccapanel/Rebecca/dev/scripts/rebecca/rebecca-node.sh"

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

// DiscoveryCommand reads the Rebecca install footprint in one pass: .env,
// .binary-release.json, the install mode marker, the systemd unit state, and
// the docker container state (the official script supports both modes).
func (RebeccaProvider) DiscoveryCommand() string {
	return `sh -c '` +
		`if [ "$(id -u)" -eq 0 ]; then SUDO=""; elif sudo -n true 2>/dev/null; then SUDO="sudo -n"; else SUDO=""; fi; ` +
		`if [ -d ` + rebeccaAppDir + ` ] || command -v rebecca-node >/dev/null 2>&1; then ` +
		`printf "=REBECCA=\n"; ` +
		`printf "=ENVSTART=\n"; $SUDO cat ` + rebeccaAppDir + `/.env 2>/dev/null || true; printf "\n=ENVEND=\n"; ` +
		`printf "=RELEASESTART=\n"; $SUDO cat ` + rebeccaAppDir + `/.binary-release.json 2>/dev/null || true; printf "\n=RELEASEEND=\n"; ` +
		`printf "=MODE=%s=\n" "$($SUDO cat ` + rebeccaAppDir + `/.install-mode 2>/dev/null)"; ` +
		`printf "=SERVICE=%s=\n" "$(systemctl is-active rebecca-node 2>/dev/null)"; ` +
		`if command -v docker >/dev/null 2>&1; then ` +
		`printf "=CONTAINER=%s=\n" "$($SUDO docker ps -a --filter name=rebecca-node --format "{{.Status}}" 2>/dev/null | head -n 1)"; ` +
		`fi; ` +
		`printf "=REBECCAEND=\n"; ` +
		`fi; true'`
}

func (RebeccaProvider) ParseDiscovery(output string, collectedAt time.Time) []Snapshot {
	lines := strings.Split(output, "\n")
	if _, found := markerValueExists(lines, "=REBECCA="); !found {
		return nil
	}

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
		dataDir = rebeccaDataDir
	}

	releaseLines, _ := markerSection(lines, "=RELEASESTART=", "=RELEASEEND=")
	version, installModeFromRelease := parseRebeccaRelease(strings.Join(releaseLines, "\n"))

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

	evidence := []string{"Install directory: " + rebeccaAppDir}
	if len(env) > 0 {
		evidence = append(evidence, fmt.Sprintf("Config: %s/.env (service port %s, protocol %s)", rebeccaAppDir, servicePort, protocol))
	}
	if version != "" {
		evidence = append(evidence, "Version from .binary-release.json: "+version)
	}
	if serviceState != "" {
		evidence = append(evidence, "systemd rebecca-node: "+serviceState)
	}
	if containerStatus != "" {
		evidence = append(evidence, "Docker container rebecca-node: "+containerStatus)
	}

	confidence := "medium"
	if len(env) > 0 || version != "" {
		confidence = "high"
	}

	return []Snapshot{normalizeSnapshot(Snapshot{
		NodeType:     rebeccaType,
		ServiceName:  "rebecca-node",
		InstallMode:  installMode,
		Version:      version,
		HealthStatus: health,
		ActivePorts:  uniqueStrings([]string{servicePort, apiPort}),
		ServicePort:  servicePort,
		APIPort:      apiPort,
		Protocol:     protocol,
		DataDir:      dataDir,
		Confidence:   confidence,
		Evidence:     evidence,
		CollectedAt:  collectedAt,
	})}
}

// markerValueExists reports whether a bare marker line (e.g. "=REBECCA=") is present.
func markerValueExists(lines []string, marker string) (string, bool) {
	for _, line := range lines {
		if strings.TrimSpace(line) == marker {
			return marker, true
		}
	}
	return "", false
}

// parseRebeccaRelease extracts the version tag and install mode from
// .binary-release.json (fields "tag" and "install_mode").
func parseRebeccaRelease(raw string) (version, installMode string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	var release struct {
		Tag         string `json:"tag"`
		InstallMode string `json:"install_mode"`
	}
	if err := json.Unmarshal([]byte(raw), &release); err != nil {
		return "", ""
	}
	return strings.TrimSpace(release.Tag), strings.ToLower(strings.TrimSpace(release.InstallMode))
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

// ── Installation (dev/beta channel) ────────────────────────────────────────────
//
// Rebecca's install model is the OPPOSITE of PasarGuard's. PasarGuard generates
// an API key + self-signed cert on the node and the panel reads them back.
// Rebecca does not hand anything back: the USER takes the node certificate from
// their Rebecca panel and provides it to the installer. So the install input is
// the certificate plus the two ports, and there is no readback step.
//
// How rebecca-node.sh "@ install --dev" consumes its inputs (verified against
// the script): with the default flavor it installs in DOCKER mode, and
// install_rebecca_node() reads stdin in this exact order —
//  1. the client certificate PEM, line by line, terminated by a BLANK line;
//  2. the SERVICE_PORT (the protocol is auto-set to REST, no prompt);
//  3. the XRAY_API_PORT (must differ from SERVICE_PORT).
// It then writes docker-compose.yml and runs `compose up -d` (detached).
//
// The certificate is multi-line PEM, so a bare interactive `read` can't carry
// it. We deliver it robustly by base64-encoding the PEM on our side and
// decoding it into a temp stdin file on the remote, then appending the blank
// line + the two ports. base64 has no quotes/newlines/metacharacters, so it is
// safe inside the outer `sh -c '...'` (no single quote ever appears — the
// TestGeneratedShellSyntax guard) and immune to set -e / non-tty / SSH issues.

// RebeccaInstallConfig carries the pre-install choices for a Rebecca dev install.
// Certificate is the client certificate PEM obtained from the Rebecca panel.
type RebeccaInstallConfig struct {
	Channel     string
	ServicePort string
	APIPort     string
	Certificate string
}

// Normalize fills port defaults and validates each field, returning a cleaned
// copy. Field-keyed validation lives in normalizeInstallInput; this guards the
// command builder so a malformed config can never reach the shell.
func (c RebeccaInstallConfig) Normalize() (RebeccaInstallConfig, error) {
	out := RebeccaInstallConfig{
		Channel:     strings.ToLower(strings.TrimSpace(c.Channel)),
		ServicePort: strings.TrimSpace(c.ServicePort),
		APIPort:     strings.TrimSpace(c.APIPort),
		Certificate: strings.TrimSpace(c.Certificate),
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
	if !looksLikePEMCertificate(out.Certificate) {
		return RebeccaInstallConfig{}, fmt.Errorf("nodes: rebecca: certificate is not a PEM certificate block")
	}
	if !rebeccaChannelEnabled(out.Channel) {
		return RebeccaInstallConfig{}, fmt.Errorf("nodes: rebecca: channel %q is not available for install", out.Channel)
	}
	return out, nil
}

// looksLikePEMCertificate reports whether s decodes to a PEM block whose type is
// a CERTIFICATE — mirroring the script's grep for BEGIN/END CERTIFICATE while
// being stricter (it must actually decode).
func looksLikePEMCertificate(s string) bool {
	block, _ := pem.Decode([]byte(strings.TrimSpace(s)))
	return block != nil && strings.Contains(strings.ToUpper(block.Type), "CERTIFICATE")
}

// normalizeInstallInput validates the raw install form fields for a Rebecca
// install and returns the resolved config plus field-keyed validation errors
// (the values are i18n keys the handler translates). Empty errors means valid.
func (RebeccaProvider) normalizeInstallInput(in installFormInput) (RebeccaInstallConfig, ValidationErrors) {
	errs := ValidationErrors{}

	cfg := RebeccaInstallConfig{
		Channel:     strings.ToLower(strings.TrimSpace(in.Channel)),
		ServicePort: strings.TrimSpace(in.ServicePort),
		APIPort:     strings.TrimSpace(in.APIPort),
		Certificate: strings.TrimSpace(in.Certificate),
	}
	if cfg.Channel == "" {
		cfg.Channel = channelDev
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
	if cfg.Certificate == "" {
		errs["certificate"] = "nodes.err_cert_required"
	} else if !looksLikePEMCertificate(cfg.Certificate) {
		errs["certificate"] = "nodes.err_cert_pem"
	}
	if !rebeccaChannelEnabled(cfg.Channel) {
		errs["channel"] = "nodes.err_channel_disabled"
	}
	if errs.HasAny() {
		return RebeccaInstallConfig{}, errs
	}

	normalized, err := cfg.Normalize()
	if err != nil {
		errs["certificate"] = "nodes.err_cert_pem"
		return RebeccaInstallConfig{}, errs
	}
	return normalized, errs
}

// InstallCommand downloads and runs the official rebecca-node dev install
// script, feeding the certificate (base64-decoded into a temp stdin file) and
// the two ports the way the docker install path reads them.
func (RebeccaProvider) InstallCommand(cfg RebeccaInstallConfig) (string, error) {
	normalized, err := cfg.Normalize()
	if err != nil {
		return "", err
	}
	scriptURL := rebeccaDevScriptURL // only dev wired today; stable adds its URL here

	// The certificate never appears literally in the command — only as base64,
	// which carries no quotes/newlines and so is safe inside sh -c '...'.
	certB64 := base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(normalized.Certificate) + "\n"))

	command := `sh -c '` + sudoPreamble +
		`SCRIPT="$(mktemp /tmp/nodexia-rebecca-node.XXXXXX)" || exit 1; ` +
		`if command -v curl >/dev/null 2>&1; then curl -fsSL ` + scriptURL + ` -o "$SCRIPT" || { echo "download failed" >&2; rm -f "$SCRIPT"; exit 85; }; ` +
		`elif command -v wget >/dev/null 2>&1; then wget -qO "$SCRIPT" ` + scriptURL + ` || { echo "download failed" >&2; rm -f "$SCRIPT"; exit 85; }; ` +
		`else echo "curl or wget is required to install" >&2; rm -f "$SCRIPT"; exit 85; fi; ` +
		// Build the stdin the script expects: certificate, a blank line to end
		// the cert read loop, then SERVICE_PORT and XRAY_API_PORT.
		`IN="$(mktemp /tmp/nodexia-rebecca-in.XXXXXX)" || { rm -f "$SCRIPT"; exit 1; }; ` +
		`printf "%s" "` + certB64 + `" | base64 -d > "$IN" || { echo "certificate decode failed" >&2; rm -f "$SCRIPT" "$IN"; exit 1; }; ` +
		`printf "\n` + normalized.ServicePort + `\n` + normalized.APIPort + `\n" >> "$IN"; ` +
		`TMO=""; if command -v timeout >/dev/null 2>&1; then TMO="timeout ` + rebeccaInstallScriptTimeout + `"; fi; ` +
		// --name pins the instance to rebecca-node (discovery keys on it). The
		// COMMAND (install) must be parsed before --name, so order matters.
		`$TMO $SUDO bash "$SCRIPT" install --name ` + rebeccaInstallName + ` --dev <"$IN"; ` +
		`STATUS=$?; rm -f "$SCRIPT" "$IN"; ` +
		`if [ "$STATUS" -ne 0 ]; then echo "[rebecca-node install script exited with status $STATUS]" >&2; fi; ` +
		`exit $STATUS'`
	return command, nil
}

// BuildInstallPlan assembles the Rebecca dev install procedure: a single
// streamed step that runs the official script. There is no configure step and
// no readback — the certificate came from the user, not the node.
func (p RebeccaProvider) BuildInstallPlan(in installFormInput) (InstallPlan, ValidationErrors) {
	cfg, errs := p.normalizeInstallInput(in)
	if errs.HasAny() {
		return InstallPlan{}, errs
	}
	installCmd, err := p.InstallCommand(cfg)
	if err != nil {
		errs["certificate"] = "nodes.err_cert_pem"
		return InstallPlan{}, errs
	}
	plan := InstallPlan{
		Steps: []InstallStep{
			{Command: installCmd, Timeout: installCommandTimeout},
		},
	}
	return plan, errs
}
