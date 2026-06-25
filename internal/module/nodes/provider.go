package nodes

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Action describes one management operation a provider supports for a node.
// Commands always go through the official node CLI (pg-node / rebecca-node),
// never hand-rolled docker or systemctl invocations.
type Action struct {
	Key     string
	Label   string
	Icon    string        // lucide icon name rendered on the action button
	Danger  bool          // destructive: the UI asks for confirmation first
	Timeout time.Duration // SSH command timeout for this action
}

// Provider is the driver for one node family (PasarGuard, Rebecca, ...).
// Adding support for a new node type means implementing this interface and
// appending the provider to DefaultProviders — discovery, the management
// actions, and the UI cards all derive from it.
type Provider interface {
	// Type is the stable node_type identifier persisted in node_snapshots.
	Type() string
	// DisplayName is the human-facing product name.
	DisplayName() string
	// DiscoveryCommand returns a single read-only shell probe that gathers
	// every piece of evidence the provider needs (configuration files,
	// runtime state) in one SSH round trip.
	DiscoveryCommand() string
	// ParseDiscovery turns the probe output into zero or more node snapshots.
	// Node names are read from the remote configuration — never hardcoded —
	// so any number of instances can coexist.
	ParseDiscovery(output string, collectedAt time.Time) []Snapshot
	// Actions lists the management operations supported for each node.
	Actions() []Action
	// ActionCommand builds the official-CLI command for one node action and
	// returns the timeout it should run under.
	ActionCommand(nodeName, actionKey string) (string, time.Duration, error)
	// SupportsInstall reports whether Nodexia can install new instances of
	// this node type from the panel.
	SupportsInstall() bool
}

func DefaultProviders() []Provider {
	return []Provider{
		PasarGuardProvider{},
		RebeccaProvider{},
	}
}

func ProviderByType(providers []Provider, nodeType string) (Provider, bool) {
	nodeType = strings.TrimSpace(nodeType)
	for _, provider := range providers {
		if provider.Type() == nodeType {
			return provider, true
		}
	}
	return nil, false
}

func actionByKey(actions []Action, key string) (Action, bool) {
	key = strings.TrimSpace(key)
	for _, action := range actions {
		if action.Key == key {
			return action, true
		}
	}
	return Action{}, false
}

// Non-interactive SSH exit codes produced by the command preambles, mirroring
// the bulk module's convention (88 = sudo locked out).
const (
	exitSudoPassword = 88
	exitMissingCLI   = 86
	exitNoDownloader = 85
	// exitRemoteTimeout is GNU timeout's exit code; the PasarGuard install
	// script tails container logs forever after installing, so the install
	// command is bounded remotely and 124 does NOT mean the install failed.
	exitRemoteTimeout = 124
)

// sudoPreamble performs a non-interactive privilege check: already root, or
// passwordless sudo.  Exit 88 lets callers distinguish "sudo wants a
// password" from a real failure.
const sudoPreamble = `if [ "$(id -u)" -eq 0 ]; then SUDO=""; elif sudo -n true 2>/dev/null; then SUDO="sudo -n"; else echo "this action requires root or passwordless sudo" >&2; exit 88; fi; `

// nodeNamePattern guards every node name interpolated into a shell command.
// The charset is intentionally strict: it covers real pg-node instance names
// ("node", "node2", "pg-node") while making injection impossible.
var nodeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func ValidateNodeName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("nodes: node name is required")
	}
	if !nodeNamePattern.MatchString(name) {
		return fmt.Errorf("nodes: invalid node name %q: use letters, digits, dot, dash, or underscore (max 64 chars)", name)
	}
	return nil
}

// ── Shared config-file parsing helpers ───────────────────────────────────────

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

// markerSections splits probe output into sections delimited by
// "=<START>=" / "=<END>=" marker lines, returning the lines between each pair.
func markerSection(lines []string, start, end string) ([]string, bool) {
	var section []string
	inside, seen := false, false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case start:
			inside, seen = true, true
		case end:
			inside = false
		default:
			if inside {
				section = append(section, line)
			}
		}
	}
	return section, seen
}

// markerValue extracts "<value>" from the first line shaped "=<KEY>=<value>=".
func markerValue(lines []string, key string) (string, bool) {
	prefix := "=" + key + "="
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) && strings.HasSuffix(line, "=") {
			return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, prefix), "=")), true
		}
	}
	return "", false
}

// ── Small string utilities shared across providers ───────────────────────────

func containsString(values []string, target string) bool {
	for _, v := range values {
		if strings.TrimSpace(v) == target {
			return true
		}
	}
	return false
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
