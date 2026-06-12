package nodes

import (
	"encoding/json"
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
)

func (RebeccaProvider) Type() string        { return rebeccaType }
func (RebeccaProvider) DisplayName() string { return "Rebecca" }

func (RebeccaProvider) SupportsInstall() bool { return false }

// DiscoveryCommand reads the Rebecca install footprint in one pass: .env,
// .binary-release.json, the install mode marker, the systemd unit state, and
// the docker container state (the official script supports both modes).
func (RebeccaProvider) DiscoveryCommand() string {
	return `sh -c '` +
		`if [ -d ` + rebeccaAppDir + ` ] || command -v rebecca-node >/dev/null 2>&1; then ` +
		`printf "=REBECCA=\n"; ` +
		`printf "=ENVSTART=\n"; cat ` + rebeccaAppDir + `/.env 2>/dev/null || true; printf "\n=ENVEND=\n"; ` +
		`printf "=RELEASESTART=\n"; cat ` + rebeccaAppDir + `/.binary-release.json 2>/dev/null || true; printf "\n=RELEASEEND=\n"; ` +
		`printf "=MODE=%s=\n" "$(cat ` + rebeccaAppDir + `/.install-mode 2>/dev/null)"; ` +
		`printf "=SERVICE=%s=\n" "$(systemctl is-active rebecca-node 2>/dev/null)"; ` +
		`if command -v docker >/dev/null 2>&1; then ` +
		`printf "=CONTAINER=%s=\n" "$(docker ps -a --filter name=rebecca-node --format "{{.Status}}" 2>/dev/null | head -n 1)"; ` +
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
