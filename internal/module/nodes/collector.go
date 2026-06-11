package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/sshclient"
)

// ── Evidence types ────────────────────────────────────────────────────────────

type ServiceUnit struct {
	Name        string
	LoadState   string
	ActiveState string
	SubState    string
	Description string
}

type Process struct {
	PID     int
	Command string
	Args    string
}

type Listener struct {
	Port    int
	Process string
	Raw     string
}

type Container struct {
	ID     string
	Image  string
	Name   string
	Status string
	Ports  string
}

// PGNodeInstance holds per-container config for a PasarGuard Docker instance.
// Each running container produces one instance; paths follow /opt/{Name}/.
type PGNodeInstance struct {
	ContainerName string // e.g. "node", "node2"
	Image         string // e.g. "pasarguard/node:latest"
	Status        string // docker status string
	ServicePort   string // SERVICE_PORT from /opt/{Name}/.env
	APIPort       string // API_PORT from /opt/{Name}/.env
	Protocol      string // SERVICE_PROTOCOL from .env ("rest" or "grpc")
	EnvFound      bool   // true if .env was successfully read
}

// RebeccaNodeConfig holds config read from /opt/rebecca-node/.
type RebeccaNodeConfig struct {
	ServicePort string // SERVICE_PORT from .env (e.g. "62033")
	XrayAPIPort string // XRAY_API_PORT from .env (e.g. "62034")
	Protocol    string // SERVICE_PROTOCOL ("rest" or "grpc")
	Version     string // tag from .binary-release.json (e.g. "v0.2.1")
	Found       bool   // true when Rebecca install was confirmed
	ConfigRead  bool   // true when .env was actually parsed (not just defaulted)
}

// EvidenceBundle aggregates all raw evidence gathered during a discovery run.
type EvidenceBundle struct {
	Services        []ServiceUnit
	Processes       []Process
	Listeners       []Listener
	Containers      []Container
	PathHints       []string
	Dependencies    []string
	PGNodeInstances []PGNodeInstance   // one per detected PasarGuard container
	RebeccaConfig   *RebeccaNodeConfig // nil if not found
}

// ProbeReport records the result of one SSH probe command.
type ProbeReport struct {
	Label   string
	Command string
	Result  sshclient.CommandResult
	Error   error
}

// ── Detector interface ────────────────────────────────────────────────────────

type Detector interface {
	Name() string
	Detect(bundle EvidenceBundle, collectedAt time.Time) []Snapshot
}

func DefaultDetectors() []Detector {
	return []Detector{
		PasarGuardDetector{},
		RebeccaDetector{},
		PortSignatureDetector{},
		RuntimeSignatureDetector{},
	}
}

// ── Probe commands ────────────────────────────────────────────────────────────

// pgConfigsCmd discovers every PasarGuard container and reads its /opt/{name}/.env.
// Each instance is wrapped between "=PG={name}={image}=" and "=PGEND=" markers.
const pgConfigsCmd = `sh -c '` +
	`command -v docker >/dev/null 2>&1 || exit 0; ` +
	`docker ps --format "{{.Names}} {{.Image}} {{.Status}}" 2>/dev/null | ` +
	`while read name image status; do ` +
	`case "$image" in *pasarguard*|*pg-node*) ` +
	`printf "=PG=%s=%s=\n" "$name" "$image"; ` +
	`cat "/opt/$name/.env" 2>/dev/null || true; ` +
	`printf "=PGEND=\n";; ` +
	`esac; ` +
	`done` +
	`'`

// rebeccaConfigCmd reads Rebecca node config files under /opt/rebecca-node/.
const rebeccaConfigCmd = `sh -c '` +
	`if [ -f /opt/rebecca-node/.env ]; then ` +
	`printf "=REBECCA_ENV=\n"; cat /opt/rebecca-node/.env; printf "=REBECCA_ENV_END=\n"; ` +
	`fi; ` +
	`if [ -f /opt/rebecca-node/.binary-release.json ]; then ` +
	`printf "=REBECCA_RELEASE=\n"; cat /opt/rebecca-node/.binary-release.json; printf "=REBECCA_RELEASE_END=\n"; ` +
	`fi; true` +
	`'`

// ── Collection entry point ────────────────────────────────────────────────────

func Collect(ctx context.Context, sshService *sshclient.Service, req sshclient.CommandRequest, detectors []Detector) ([]Snapshot, []ProbeReport, error) {
	if len(detectors) == 0 {
		detectors = DefaultDetectors()
	}

	probeSpecs := []struct {
		label   string
		command string
	}{
		{
			label:   "services",
			command: `sh -c 'systemctl list-units --type=service --all --no-legend --no-pager 2>/dev/null || true'`,
		},
		{
			label:   "processes",
			command: `sh -c 'ps -eo pid=,comm=,args= 2>/dev/null || true'`,
		},
		{
			label:   "listeners",
			command: `sh -c 'ss -lntpH 2>/dev/null || ss -lntp 2>/dev/null || true'`,
		},
		{
			label: "containers",
			command: `sh -c 'if command -v docker >/dev/null 2>&1; then ` +
				`printf "__docker_present__\n"; ` +
				`docker ps --format "{{.ID}}\t{{.Image}}\t{{.Names}}\t{{.Status}}\t{{.Ports}}" 2>/dev/null || true; ` +
				`else printf "__docker_missing__\n"; fi'`,
		},
		{
			label: "paths",
			command: `sh -c 'for p in ` +
				`/opt/rebecca-node /var/lib/rebecca-node ` +
				`/usr/local/bin/rebecca-node /usr/bin/rebecca-node ` +
				`/etc/systemd/system/rebecca-node.service ` +
				`/etc/systemd/system/rebecca.service ` +
				`/etc/systemd/system/pg-node.service ` +
				`/opt/pg-node /var/lib/pg-node; ` +
				`do [ -e "$p" ] && printf "%s\n" "$p"; done'`,
		},
		{label: "pg-configs", command: pgConfigsCmd},
		{label: "rebecca-config", command: rebeccaConfigCmd},
	}

	bundle := EvidenceBundle{}
	reports := make([]ProbeReport, 0, len(probeSpecs))
	var collectedAt time.Time

	for _, spec := range probeSpecs {
		result, err := sshService.RunCommand(ctx, sshclient.CommandRequest{
			ConnectionRequest: req.ConnectionRequest,
			Command:           spec.command,
			CommandTimeout:    req.CommandTimeout,
		})
		if collectedAt.IsZero() && !result.CompletedAt.IsZero() {
			collectedAt = result.CompletedAt
		}
		reports = append(reports, ProbeReport{
			Label:   spec.label,
			Command: spec.command,
			Result:  result,
			Error:   err,
		})

		switch spec.label {
		case "services":
			bundle.Services = parseServices(result.Stdout)
			if err == nil {
				bundle.Dependencies = append(bundle.Dependencies, "systemctl:available")
			}
		case "processes":
			bundle.Processes = parseProcesses(result.Stdout)
			if err == nil {
				bundle.Dependencies = append(bundle.Dependencies, "ps:available")
			}
		case "listeners":
			bundle.Listeners = parseListeners(result.Stdout)
			if err == nil {
				bundle.Dependencies = append(bundle.Dependencies, "ss:available")
			}
		case "containers":
			containers, dependency := parseContainers(result.Stdout)
			bundle.Containers = containers
			if dependency != "" {
				bundle.Dependencies = append(bundle.Dependencies, dependency)
			}
		case "paths":
			bundle.PathHints = parsePaths(result.Stdout)
		case "pg-configs":
			bundle.PGNodeInstances = parsePGNodeConfigs(result.Stdout)
		case "rebecca-config":
			bundle.RebeccaConfig = parseRebeccaConfig(result.Stdout)
		}
	}

	if collectedAt.IsZero() {
		collectedAt = time.Now().UTC()
	}
	bundle.Dependencies = uniqueStrings(bundle.Dependencies)

	snapshots := make([]Snapshot, 0)
	for _, detector := range detectors {
		snapshots = append(snapshots, detector.Detect(bundle, collectedAt)...)
	}

	return dedupeSnapshots(snapshots), reports, nil
}

// ── Config file parsers ───────────────────────────────────────────────────────

// parsePGNodeConfigs parses the pg-configs probe output.
// Input format: "=PG={name}={image}=\n<env file lines>\n=PGEND=\n" per instance.
func parsePGNodeConfigs(output string) []PGNodeInstance {
	var instances []PGNodeInstance
	var current *PGNodeInstance
	var envLines []string

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "=PG=") && strings.HasSuffix(line, "=") {
			inner := strings.TrimSuffix(strings.TrimPrefix(line, "=PG="), "=")
			name, image, _ := strings.Cut(inner, "=")
			current = &PGNodeInstance{
				ContainerName: strings.TrimSpace(name),
				Image:         strings.TrimSpace(image),
			}
			envLines = nil
		} else if line == "=PGEND=" && current != nil {
			env := parseEnvFile(envLines)
			current.ServicePort = parsePortFromEnv(env, "SERVICE_PORT")
			current.APIPort = parsePortFromEnv(env, "API_PORT")
			current.Protocol = cleanEnvValue(env["SERVICE_PROTOCOL"])
			current.EnvFound = len(env) > 0
			if current.ContainerName != "" {
				instances = append(instances, *current)
			}
			current = nil
			envLines = nil
		} else if current != nil {
			envLines = append(envLines, line)
		}
	}
	return instances
}

// parseRebeccaConfig parses the rebecca-config probe output.
func parseRebeccaConfig(output string) *RebeccaNodeConfig {
	var envSection, releaseSection string
	inEnv, inRelease := false, false
	envMarkerSeen, releaseMarkerSeen := false, false

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch line {
		case "=REBECCA_ENV=":
			inEnv = true
			envMarkerSeen = true
		case "=REBECCA_ENV_END=":
			inEnv = false
		case "=REBECCA_RELEASE=":
			inRelease = true
			releaseMarkerSeen = true
		case "=REBECCA_RELEASE_END=":
			inRelease = false
		default:
			if inEnv {
				envSection += line + "\n"
			}
			if inRelease {
				releaseSection += line + "\n"
			}
		}
	}

	// No markers at all means the probe found nothing (files don't exist or no access).
	if !envMarkerSeen && !releaseMarkerSeen {
		return nil
	}

	// Markers were present — Rebecca is installed. Even if cat failed (permission
	// denied), we return a config so buildRebeccaSnapshot can apply defaults.
	cfg := &RebeccaNodeConfig{Found: true}

	if envSection != "" {
		env := parseEnvFile(strings.Split(envSection, "\n"))
		cfg.ServicePort = parsePortFromEnv(env, "SERVICE_PORT")
		cfg.XrayAPIPort = parsePortFromEnv(env, "XRAY_API_PORT")
		cfg.Protocol = cleanEnvValue(env["SERVICE_PROTOCOL"])
		cfg.ConfigRead = true
	}

	if strings.TrimSpace(releaseSection) != "" {
		var release struct {
			Tag string `json:"tag"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(releaseSection)), &release); err == nil && release.Tag != "" {
			cfg.Version = release.Tag
			cfg.ConfigRead = true
		}
	}

	return cfg
}

// parseEnvFile parses KEY = VALUE formatted lines into a map.
// Lines beginning with # are treated as comments. Quotes are stripped from values.
func parseEnvFile(lines []string) map[string]string {
	env := map[string]string{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		value = strings.TrimSpace(value)
		if key != "" {
			env[key] = value
		}
	}
	return env
}

// parsePortFromEnv reads a named key from an env map and returns a valid port string.
func parsePortFromEnv(env map[string]string, key string) string {
	val := strings.TrimSpace(env[key])
	if val == "" {
		return ""
	}
	port, err := strconv.Atoi(val)
	if err != nil || port < 1 || port > 65535 {
		return ""
	}
	return strconv.Itoa(port)
}

// cleanEnvValue strips surrounding quotes and trims whitespace.
func cleanEnvValue(value string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(value), `"'`))
}

// ── PasarGuard Detector ───────────────────────────────────────────────────────

type PasarGuardDetector struct{}

func (PasarGuardDetector) Name() string { return "pasarguard-node" }

// Detect returns one Snapshot per PasarGuard container instance.
// Priority: config-based (pg-configs probe) → container list → evidence-based fallback.
func (PasarGuardDetector) Detect(bundle EvidenceBundle, collectedAt time.Time) []Snapshot {
	// Primary: config-based — one snapshot per container with real port/protocol data.
	if len(bundle.PGNodeInstances) > 0 {
		snapshots := make([]Snapshot, 0, len(bundle.PGNodeInstances))
		for _, inst := range bundle.PGNodeInstances {
			snapshots = append(snapshots, buildPGSnapshot(bundle, inst, collectedAt))
		}
		return snapshots
	}

	// Fallback A: enumerate matching containers from docker ps output.
	var matched []Container
	for _, c := range bundle.Containers {
		if matchKeywords(c.Image+" "+c.Name, "pasarguard/node", "pasarguard", "pg-node") {
			matched = append(matched, c)
		}
	}
	if len(matched) > 0 {
		snapshots := make([]Snapshot, 0, len(matched))
		for _, c := range matched {
			snapshots = append(snapshots, buildPGSnapshotFromContainer(bundle, c, collectedAt))
		}
		return snapshots
	}

	// Fallback B: general evidence heuristic.
	spec := familySpec{
		NodeType:          "pasarguard-node",
		ServiceKeywords:   []string{"pasarguard", "pg-node"},
		ProcessKeywords:   []string{"pasarguard", "pg-node"},
		ContainerKeywords: []string{"pasarguard/node", "pasarguard", "pg-node"},
		PathKeywords:      []string{"/var/lib/pg-node", "/opt/pg-node", "pg-node", "pasarguard"},
		InstallMode:       "docker",
	}
	snapshot, ok := detectFamily(bundle, spec, collectedAt)
	if !ok {
		return nil
	}
	return []Snapshot{snapshot}
}

func buildPGSnapshot(bundle EvidenceBundle, inst PGNodeInstance, collectedAt time.Time) Snapshot {
	// Prefer .env-configured ports; fall back to PasarGuard defaults.
	servicePort := inst.ServicePort
	if servicePort == "" {
		servicePort = "62050"
	}
	apiPort := inst.APIPort
	if apiPort == "" {
		apiPort = "62051"
	}
	activePorts := uniqueStrings([]string{servicePort, apiPort})

	// Xray proxy ports from container port mappings (non-management ports).
	containerPorts := extractContainerPorts(findContainerPorts(bundle, inst.ContainerName))
	xrayPorts := uniqueStrings(containerXrayPorts(containerPorts, activePorts))

	// Health: check if container is actually running.
	health := "detected"
	for _, c := range bundle.Containers {
		if strings.EqualFold(c.Name, inst.ContainerName) {
			if strings.HasPrefix(strings.ToLower(c.Status), "up") {
				health = "running"
			}
			break
		}
	}

	evidence := []string{
		fmt.Sprintf("Docker container: %s (image: %s)", inst.ContainerName, inst.Image),
	}
	if inst.EnvFound {
		evidence = append(evidence, fmt.Sprintf("Config: /opt/%s/.env (service port %s, API port %s)", inst.ContainerName, servicePort, apiPort))
	}
	if inst.Protocol != "" {
		evidence = append(evidence, "Protocol: "+inst.Protocol)
	}
	for _, l := range bundle.Listeners {
		p := strconv.Itoa(l.Port)
		if containsString(activePorts, p) {
			evidence = append(evidence, fmt.Sprintf("Port %d listening via %s", l.Port, l.Process))
			if health == "detected" {
				health = "running"
			}
		}
	}

	return normalizeSnapshot(Snapshot{
		NodeType:     "pasarguard-node",
		ServiceName:  inst.ContainerName,
		InstallMode:  "docker",
		Version:      extractImageTag(inst.Image),
		HealthStatus: health,
		ActivePorts:  activePorts,
		XrayPorts:    xrayPorts,
		ServicePort:  servicePort,
		APIPort:      apiPort,
		Protocol:     inst.Protocol,
		Confidence:   confidenceForPG(inst),
		Dependencies: bundle.Dependencies,
		Evidence:     evidence,
		CollectedAt:  collectedAt,
	})
}

func confidenceForPG(inst PGNodeInstance) string {
	if inst.EnvFound {
		return "high"
	}
	return "medium"
}

func buildPGSnapshotFromContainer(bundle EvidenceBundle, c Container, collectedAt time.Time) Snapshot {
	containerPorts := extractContainerPorts(c.Ports)
	activePorts := uniqueStrings(append(containerMgmtPorts(containerPorts), "62050", "62051"))
	xrayPorts := uniqueStrings(containerXrayPorts(containerPorts, activePorts))

	health := "detected"
	if strings.HasPrefix(strings.ToLower(c.Status), "up") {
		health = "running"
	}
	for _, l := range bundle.Listeners {
		p := strconv.Itoa(l.Port)
		if containsString(activePorts, p) && health == "detected" {
			health = "running"
		}
	}

	evidence := []string{
		fmt.Sprintf("Docker container: %s (image: %s)", c.Name, c.Image),
	}

	return normalizeSnapshot(Snapshot{
		NodeType:     "pasarguard-node",
		ServiceName:  firstNonEmpty(c.Name, c.Image),
		InstallMode:  "docker",
		Version:      extractImageTag(c.Image),
		HealthStatus: health,
		ActivePorts:  activePorts,
		XrayPorts:    xrayPorts,
		ServicePort:  "62050",
		APIPort:      "62051",
		Confidence:   "medium",
		Dependencies: bundle.Dependencies,
		Evidence:     evidence,
		CollectedAt:  collectedAt,
	})
}

// ── Rebecca Detector ──────────────────────────────────────────────────────────

type RebeccaDetector struct{}

func (RebeccaDetector) Name() string { return "rebecca-node" }

// Detect returns a Rebecca node snapshot.
// Priority: config-based → evidence-based (synthesize default config) → nil.
func (RebeccaDetector) Detect(bundle EvidenceBundle, collectedAt time.Time) []Snapshot {
	if bundle.RebeccaConfig != nil && bundle.RebeccaConfig.Found {
		return []Snapshot{buildRebeccaSnapshot(bundle, collectedAt)}
	}

	// If any evidence confirms Rebecca is installed but the config probe couldn't
	// read the files (e.g. permission denied), synthesize a default config and let
	// buildRebeccaSnapshot apply the correct Rebecca port defaults (62033/62034).
	if rebeccaEvidenceFound(bundle) {
		bundle.RebeccaConfig = &RebeccaNodeConfig{Found: true}
		return []Snapshot{buildRebeccaSnapshot(bundle, collectedAt)}
	}

	return nil
}

func rebeccaEvidenceFound(bundle EvidenceBundle) bool {
	keywords := []string{"rebecca-node", "rebecca"}
	for _, svc := range bundle.Services {
		if matchKeywords(svc.Name+" "+svc.Description, keywords...) {
			return true
		}
	}
	for _, proc := range bundle.Processes {
		if matchKeywords(proc.Command+" "+proc.Args, keywords...) {
			return true
		}
	}
	for _, path := range bundle.PathHints {
		if matchKeywords(path, keywords...) {
			return true
		}
	}
	for _, c := range bundle.Containers {
		if matchKeywords(c.Image+" "+c.Name, keywords...) {
			return true
		}
	}
	return false
}

func buildRebeccaSnapshot(bundle EvidenceBundle, collectedAt time.Time) Snapshot {
	cfg := bundle.RebeccaConfig

	servicePort := cfg.ServicePort
	if servicePort == "" {
		servicePort = "62033"
	}
	xrayAPIPort := cfg.XrayAPIPort
	if xrayAPIPort == "" {
		xrayAPIPort = "62034"
	}
	activePorts := uniqueStrings([]string{servicePort, xrayAPIPort})

	var evidence []string
	if cfg.ConfigRead {
		evidence = append(evidence, "Config: /opt/rebecca-node/ (env + release files)")
	} else {
		evidence = append(evidence, "Install path: /opt/rebecca-node/ (config unreadable, using defaults)")
	}
	if cfg.Version != "" {
		evidence = append(evidence, "Version from .binary-release.json: "+cfg.Version)
	}
	if cfg.Protocol != "" {
		evidence = append(evidence, "Protocol: "+cfg.Protocol)
	}

	// Health: services → processes → listeners.
	health := "detected"
	serviceName := "rebecca-node"
	for _, svc := range bundle.Services {
		if matchKeywords(svc.Name, "rebecca-node", "rebecca") {
			evidence = append(evidence, fmt.Sprintf("Service %s: %s/%s", svc.Name, svc.ActiveState, svc.SubState))
			serviceName = svc.Name
			if svc.ActiveState == "active" && svc.SubState == "running" {
				health = "running"
			} else if health == "detected" {
				health = "detected"
			}
			break
		}
	}
	if health == "detected" {
		for _, proc := range bundle.Processes {
			if matchKeywords(proc.Command+" "+proc.Args, "rebecca-node", "rebecca") {
				evidence = append(evidence, fmt.Sprintf("Process %s running (pid %d)", proc.Command, proc.PID))
				health = "running"
				break
			}
		}
	}

	// Listener evidence + xray port discovery.
	var xrayPorts []string
	for _, l := range bundle.Listeners {
		p := strconv.Itoa(l.Port)
		if containsString(activePorts, p) {
			evidence = append(evidence, fmt.Sprintf("Port %d listening via %s", l.Port, l.Process))
			if health == "detected" {
				health = "running"
			}
		} else if matchKeywords(l.Process, "rebecca", "xray") {
			xrayPorts = append(xrayPorts, p)
		}
	}

	// Path hints.
	for _, path := range bundle.PathHints {
		if matchKeywords(path, "rebecca") {
			evidence = append(evidence, "Filesystem: "+path)
		}
	}

	confidence := "high"
	if !cfg.ConfigRead {
		confidence = "medium"
	}

	return normalizeSnapshot(Snapshot{
		NodeType:     "rebecca-node",
		ServiceName:  serviceName,
		InstallMode:  "binary",
		Version:      cfg.Version,
		HealthStatus: health,
		ActivePorts:  activePorts,
		XrayPorts:    uniqueStrings(xrayPorts),
		ServicePort:  servicePort,
		APIPort:      xrayAPIPort,
		Protocol:     cfg.Protocol,
		Confidence:   confidence,
		Dependencies: bundle.Dependencies,
		Evidence:     evidence,
		CollectedAt:  collectedAt,
	})
}

// ── Port-signature Detector ───────────────────────────────────────────────────

type PortSignatureDetector struct{}

func (PortSignatureDetector) Name() string { return "port-signature" }

func (PortSignatureDetector) Detect(bundle EvidenceBundle, collectedAt time.Time) []Snapshot {
	mgmt, xray, apiPort, evidence := collectPortsByProcess(bundle)
	if len(mgmt) == 0 {
		return nil
	}

	confidence := "low"
	if containsString(mgmt, "62050") && containsString(mgmt, "62051") {
		confidence = "medium"
	}

	processes := make([]string, 0)
	for _, l := range bundle.Listeners {
		if l.Port == 62050 || l.Port == 62051 {
			processes = append(processes, l.Process)
		}
	}
	serviceName := firstNonEmpty(processes...)
	if serviceName == "" {
		serviceName = "port signature"
	}

	servicePort := ""
	if containsString(mgmt, "62050") {
		servicePort = "62050"
	}

	return []Snapshot{{
		NodeType:     "port-signature",
		ServiceName:  serviceName,
		InstallMode:  detectInstallMode(bundle),
		HealthStatus: "running",
		ActivePorts:  mgmt,
		XrayPorts:    xray,
		ServicePort:  servicePort,
		APIPort:      apiPort,
		Confidence:   confidence,
		Dependencies: bundle.Dependencies,
		Evidence:     evidence,
		CollectedAt:  collectedAt,
	}}
}

// ── Runtime-signature Detector ────────────────────────────────────────────────

type RuntimeSignatureDetector struct{}

func (RuntimeSignatureDetector) Name() string { return "runtime-signature" }

func (RuntimeSignatureDetector) Detect(bundle EvidenceBundle, collectedAt time.Time) []Snapshot {
	keywords := []string{"rebecca", "pasarguard", "pg-node", "xray", "xtls", "wireguard"}
	evidence := make([]string, 0)
	serviceNames := make([]string, 0)

	for _, svc := range bundle.Services {
		if matchKeywords(svc.Name+" "+svc.Description, keywords...) {
			evidence = append(evidence, fmt.Sprintf("Service: %s (%s/%s)", svc.Name, svc.ActiveState, svc.SubState))
			serviceNames = append(serviceNames, svc.Name)
		}
	}
	for _, proc := range bundle.Processes {
		if matchKeywords(proc.Command+" "+proc.Args, keywords...) {
			evidence = append(evidence, fmt.Sprintf("Process: %s (pid %d)", proc.Command, proc.PID))
			serviceNames = append(serviceNames, proc.Command)
		}
	}
	for _, c := range bundle.Containers {
		if matchKeywords(c.Image+" "+c.Name, keywords...) {
			evidence = append(evidence, fmt.Sprintf("Container: %s (image: %s)", c.Name, c.Image))
			serviceNames = append(serviceNames, c.Name)
		}
	}
	for _, path := range bundle.PathHints {
		if matchKeywords(path, keywords...) {
			evidence = append(evidence, "Filesystem: "+path)
		}
	}

	mgmt, xray, apiPort, portEvidence := collectPortsByProcess(bundle, keywords...)
	evidence = append(evidence, portEvidence...)

	if len(evidence) == 0 {
		return nil
	}

	confidence := "medium"
	if len(evidence) >= 4 {
		confidence = "high"
	}

	return []Snapshot{{
		NodeType:     "runtime-signature",
		ServiceName:  firstNonEmpty(serviceNames...),
		InstallMode:  detectInstallMode(bundle),
		HealthStatus: detectHealth(bundle),
		ActivePorts:  mgmt,
		XrayPorts:    xray,
		APIPort:      apiPort,
		Confidence:   confidence,
		Dependencies: bundle.Dependencies,
		Evidence:     evidence,
		CollectedAt:  collectedAt,
	}}
}

// ── Family-spec detection (shared evidence-based fallback) ────────────────────

type familySpec struct {
	NodeType          string
	ServiceKeywords   []string
	ProcessKeywords   []string
	ContainerKeywords []string
	PathKeywords      []string
	InstallMode       string
}

func detectFamily(bundle EvidenceBundle, spec familySpec, collectedAt time.Time) (Snapshot, bool) {
	evidence := make([]string, 0)
	serviceNames := make([]string, 0)
	containerMatched, serviceMatched, processMatched, pathMatched := 0, 0, 0, 0

	for _, svc := range bundle.Services {
		if matchKeywords(svc.Name+" "+svc.Description, spec.ServiceKeywords...) {
			evidence = append(evidence, fmt.Sprintf("Service: %s (%s/%s)", svc.Name, svc.ActiveState, svc.SubState))
			serviceNames = append(serviceNames, svc.Name)
			serviceMatched++
		}
	}
	for _, proc := range bundle.Processes {
		if matchKeywords(proc.Command+" "+proc.Args, spec.ProcessKeywords...) {
			evidence = append(evidence, fmt.Sprintf("Process: %s (pid %d)", proc.Command, proc.PID))
			serviceNames = append(serviceNames, proc.Command)
			processMatched++
		}
	}

	containerPorts := make([]string, 0)
	for _, c := range bundle.Containers {
		if matchKeywords(c.Image+" "+c.Name, spec.ContainerKeywords...) {
			evidence = append(evidence, fmt.Sprintf("Container: %s (image: %s)", c.Name, c.Image))
			serviceNames = append(serviceNames, firstNonEmpty(c.Name, c.Image))
			containerPorts = append(containerPorts, extractContainerPorts(c.Ports)...)
			containerMatched++
		}
	}
	for _, path := range bundle.PathHints {
		if matchKeywords(path, spec.PathKeywords...) {
			evidence = append(evidence, "Filesystem: "+path)
			pathMatched++
		}
	}

	if containerMatched+serviceMatched+processMatched+pathMatched == 0 {
		return Snapshot{}, false
	}

	portKeywords := uniqueStrings(append(spec.ServiceKeywords, spec.ProcessKeywords...))
	mgmt, xray, apiPort, portEvidence := collectPortsByProcess(bundle, portKeywords...)
	evidence = append(evidence, portEvidence...)
	activePorts := uniqueStrings(append(mgmt, containerMgmtPorts(containerPorts)...))
	xrayPorts := uniqueStrings(append(xray, containerXrayPorts(containerPorts, activePorts)...))
	if apiPort == "" && containsString(activePorts, "62051") {
		apiPort = "62051"
	}

	servicePort := ""
	for _, p := range []string{"62050", "62033"} {
		if containsString(activePorts, p) {
			servicePort = p
			break
		}
	}

	health := resolveFamilyHealth(bundle, spec.InstallMode, containerMatched > 0, serviceMatched+processMatched > 0, len(activePorts) > 0)

	score := 0
	if spec.InstallMode == "docker" {
		score += containerMatched * 3
		score += serviceMatched + processMatched
	} else {
		score += (serviceMatched + processMatched) * 2
		score += pathMatched
		if containerMatched > 0 {
			score++
		}
	}
	if len(activePorts) > 0 {
		score++
	}
	if len(activePorts) > 1 {
		score++
	}

	confidence := "low"
	if score >= 6 {
		confidence = "high"
	} else if score >= 3 {
		confidence = "medium"
	}

	return normalizeSnapshot(Snapshot{
		NodeType:     spec.NodeType,
		ServiceName:  firstNonEmpty(serviceNames...),
		InstallMode:  spec.InstallMode,
		HealthStatus: health,
		ActivePorts:  activePorts,
		XrayPorts:    xrayPorts,
		ServicePort:  servicePort,
		APIPort:      apiPort,
		Confidence:   confidence,
		Dependencies: bundle.Dependencies,
		Evidence:     evidence,
		CollectedAt:  collectedAt,
	}), true
}

// ── Port helpers ──────────────────────────────────────────────────────────────

// collectPortsByProcess splits matched listeners into management ports (62050/62051)
// and xray proxy ports.
func collectPortsByProcess(bundle EvidenceBundle, keywords ...string) (mgmt []string, xray []string, apiPort string, evidence []string) {
	for _, l := range bundle.Listeners {
		isManagement := l.Port == 62050 || l.Port == 62051
		matchedByProcess := len(keywords) == 0 || matchKeywords(l.Process, keywords...)
		if !matchedByProcess && !isManagement {
			continue
		}

		portValue := strconv.Itoa(l.Port)
		processName := strings.TrimSpace(l.Process)
		if processName == "" {
			processName = "listener"
		}

		if isManagement {
			mgmt = append(mgmt, portValue)
			if l.Port == 62051 {
				apiPort = portValue
			}
			evidence = append(evidence, fmt.Sprintf("Management port %d via %s", l.Port, processName))
		} else {
			xray = append(xray, portValue)
			evidence = append(evidence, fmt.Sprintf("Proxy port %d via %s", l.Port, processName))
		}
	}
	return uniqueStrings(mgmt), uniqueStrings(xray), apiPort, uniqueStrings(evidence)
}

func containerMgmtPorts(ports []string) []string {
	out := make([]string, 0)
	for _, p := range ports {
		if p == "62050" || p == "62051" {
			out = append(out, p)
		}
	}
	return out
}

func containerXrayPorts(ports []string, mgmt []string) []string {
	mgmtSet := map[string]struct{}{}
	for _, p := range mgmt {
		mgmtSet[p] = struct{}{}
	}
	out := make([]string, 0)
	for _, p := range ports {
		if _, isMgmt := mgmtSet[p]; !isMgmt {
			out = append(out, p)
		}
	}
	return out
}

// findContainerPorts returns the Ports field of a named container.
func findContainerPorts(bundle EvidenceBundle, containerName string) string {
	for _, c := range bundle.Containers {
		if strings.EqualFold(c.Name, containerName) {
			return c.Ports
		}
	}
	return ""
}

// extractImageTag returns the tag portion of "repo/image:tag", e.g. "latest".
func extractImageTag(image string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return ""
	}
	if idx := strings.LastIndex(image, ":"); idx != -1 && idx < len(image)-1 {
		return image[idx+1:]
	}
	return ""
}

// ── Probe output parsers ──────────────────────────────────────────────────────

func parseServices(output string) []ServiceUnit {
	lines := strings.Split(output, "\n")
	services := make([]ServiceUnit, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 4 {
			continue
		}
		description := ""
		if len(fields) > 4 {
			description = strings.Join(fields[4:], " ")
		}
		services = append(services, ServiceUnit{
			Name:        fields[0],
			LoadState:   fields[1],
			ActiveState: fields[2],
			SubState:    fields[3],
			Description: description,
		})
	}
	return services
}

func parseProcesses(output string) []Process {
	lines := strings.Split(output, "\n")
	processes := make([]Process, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		args := ""
		if len(fields) > 2 {
			args = strings.Join(fields[2:], " ")
		}
		processes = append(processes, Process{
			PID:     pid,
			Command: fields[1],
			Args:    args,
		})
	}
	return processes
}

func parseListeners(output string) []Listener {
	lines := strings.Split(output, "\n")
	listeners := make([]Listener, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, ":") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		port, err := parsePort(fields[3])
		if err != nil {
			continue
		}
		process := ""
		if len(fields) > 5 {
			process = strings.Join(fields[5:], " ")
		}
		listeners = append(listeners, Listener{
			Port:    port,
			Process: strings.TrimSpace(process),
			Raw:     line,
		})
	}
	return listeners
}

func parseContainers(output string) ([]Container, string) {
	lines := strings.Split(output, "\n")
	containers := make([]Container, 0)
	dependency := ""
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "__docker_present__" {
			dependency = "docker:available"
			continue
		}
		if line == "__docker_missing__" {
			dependency = "docker:missing"
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 5 {
			continue
		}
		containers = append(containers, Container{
			ID:     strings.TrimSpace(parts[0]),
			Image:  strings.TrimSpace(parts[1]),
			Name:   strings.TrimSpace(parts[2]),
			Status: strings.TrimSpace(parts[3]),
			Ports:  strings.TrimSpace(parts[4]),
		})
	}
	return containers, dependency
}

func parsePaths(output string) []string {
	return uniqueStrings(strings.Split(output, "\n"))
}

func extractContainerPorts(value string) []string {
	parts := strings.Split(value, ",")
	ports := make([]string, 0, len(parts))
	for _, part := range parts {
		if port := parseContainerPort(part); port != "" {
			ports = append(ports, port)
		}
	}
	return uniqueStrings(ports)
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
	port, err := strconv.Atoi(segment)
	if err != nil || port < 1 || port > 65535 {
		return ""
	}
	return strconv.Itoa(port)
}

// ── Health / install mode heuristics ─────────────────────────────────────────

func detectInstallMode(bundle EvidenceBundle) string {
	if len(bundle.Containers) > 0 {
		return "docker"
	}
	if len(bundle.Services) > 0 || len(bundle.Processes) > 0 || len(bundle.PathHints) > 0 {
		return "binary"
	}
	return "unknown"
}

func detectHealth(bundle EvidenceBundle) string {
	if len(bundle.Listeners) > 0 || len(bundle.Containers) > 0 {
		return "running"
	}
	if len(bundle.Services) > 0 || len(bundle.Processes) > 0 {
		return "detected"
	}
	return "unknown"
}

func resolveFamilyHealth(bundle EvidenceBundle, installMode string, containerMatched, processMatched, portsMatched bool) string {
	if portsMatched {
		return "running"
	}
	if installMode == "docker" && containerMatched {
		return "running"
	}
	if processMatched || len(bundle.Services) > 0 {
		return "detected"
	}
	return "unknown"
}

// ── Deduplication & sorting ───────────────────────────────────────────────────

func dedupeSnapshots(snapshots []Snapshot) []Snapshot {
	if len(snapshots) == 0 {
		return nil
	}
	out := make([]Snapshot, 0, len(snapshots))
	seen := map[string]struct{}{}
	for _, snapshot := range snapshots {
		snapshot = normalizeSnapshot(snapshot)
		key := snapshot.NodeType + "|" + snapshot.ServiceName + "|" + strings.Join(snapshot.ActivePorts, ",")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, snapshot)
	}
	out = filterGenericFallbacks(out)
	sort.Slice(out, func(i, j int) bool {
		li, lj := snapshotSortRank(out[i].NodeType), snapshotSortRank(out[j].NodeType)
		if li != lj {
			return li < lj
		}
		return out[i].ServiceName < out[j].ServiceName
	})
	return out
}

func filterGenericFallbacks(snapshots []Snapshot) []Snapshot {
	hasSpecific := false
	for _, s := range snapshots {
		if isSpecificNodeType(s.NodeType) {
			hasSpecific = true
			break
		}
	}
	if !hasSpecific {
		return snapshots
	}
	filtered := make([]Snapshot, 0, len(snapshots))
	for _, s := range snapshots {
		if !isGenericFallback(s.NodeType) {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func isSpecificNodeType(nodeType string) bool {
	return !isGenericFallback(nodeType) && strings.TrimSpace(nodeType) != "" && strings.TrimSpace(nodeType) != "none"
}

func isGenericFallback(nodeType string) bool {
	switch strings.TrimSpace(nodeType) {
	case "runtime-signature", "port-signature":
		return true
	default:
		return false
	}
}

func snapshotSortRank(nodeType string) int {
	switch strings.TrimSpace(nodeType) {
	case "pasarguard-node":
		return 1
	case "rebecca-node":
		return 2
	case "runtime-signature":
		return 3
	case "port-signature":
		return 4
	case "none":
		return 5
	default:
		return 10
	}
}

// ── String utilities ──────────────────────────────────────────────────────────

func parsePort(localAddress string) (int, error) {
	localAddress = strings.TrimSpace(localAddress)
	if localAddress == "" {
		return 0, fmt.Errorf("empty address")
	}
	idx := strings.LastIndex(localAddress, ":")
	if idx == -1 || idx == len(localAddress)-1 {
		return 0, fmt.Errorf("invalid address %q", localAddress)
	}
	portValue := strings.Trim(localAddress[idx+1:], "[]")
	return strconv.Atoi(portValue)
}

func containsString(values []string, target string) bool {
	for _, v := range values {
		if strings.TrimSpace(v) == target {
			return true
		}
	}
	return false
}

func matchKeywords(value string, keywords ...string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, kw := range keywords {
		if strings.Contains(value, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
