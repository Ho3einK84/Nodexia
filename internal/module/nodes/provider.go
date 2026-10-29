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

// InstallFormInput is the provider-agnostic install form payload. It is the
// single shape every provider's BuildInstallPlan consumes, so the HTTP layer
// never needs to know which provider it is talking to: each provider reads the
// fields it cares about and ignores the rest. Field values are named exactly
// after the form inputs, which keeps validation-error keys, i18n keys and HTML
// field names aligned by construction.
type InstallFormInput struct {
	NodeType    string // provider selector ("pasarguard-node" | "rebecca-node")
	NodeName    string
	ServicePort string
	APIPort     string
	Protocol    string // "rest" | "grpc" (PasarGuard)
	APIKey      string // optional UUID (PasarGuard)
	Bundle      string // PEM certificate+key bundle (Rebecca)
	Channel     string // release channel: "stable" | "dev"
	SANEntries  string // optional extra certificate SANs (PasarGuard, comma separated)
}

// ValidationErrors maps a form field name to a message. Providers return i18n
// keys here; the HTTP layer translates them before rendering, so every provider
// produces identically localized errors.
type ValidationErrors map[string]string

func (v ValidationErrors) HasAny() bool { return len(v) > 0 }

// InstallChannel is one release channel a provider can install from.
// Enabled=false renders as "coming soon" and is rejected server-side, so
// turning a channel on later is a one-line flip plus its plan branch.
type InstallChannel struct {
	Key     string // stable, dev
	Label   string // i18n key for the human label
	Enabled bool
}

// InstallDefaults is the pre-filled state of the deploy form for a provider.
// It lives on the provider so the form, the modal and the validation all agree
// on defaults without the HTTP layer hardcoding provider knowledge.
type InstallDefaults struct {
	NodeName    string
	ServicePort string
	APIPort     string
	Protocol    string
	Channel     string
}

// InstallableProvider is implemented by every provider that can be installed
// from the panel. It is the one seam installation goes through:
// BuildInstallPlan turns raw form input into an InstallPlan (validating as it
// goes), so adding a provider never touches the install runner, the job store,
// or the SSE streamer.
type InstallableProvider interface {
	Provider

	// BuildInstallPlan validates the raw install form and assembles the
	// provider-agnostic recipe runInstall executes. Field-keyed i18n keys are
	// returned when the input is invalid.
	BuildInstallPlan(in InstallFormInput) (InstallPlan, ValidationErrors)

	// InstallChannels lists the release channels this provider can install
	// from, in display order. Providers with a single channel return one entry.
	InstallChannels() []InstallChannel

	// InstallDefaults is the form's pre-filled state.
	InstallDefaults() InstallDefaults
}

func DefaultProviders() []Provider {
	return []Provider{
		PasarGuardProvider{},
		RebeccaProvider{},
	}
}

// DefaultInstallableProviders returns the install-capable providers.
func DefaultInstallableProviders() []InstallableProvider {
	return []InstallableProvider{
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

// InstallableProviderByType resolves the install-capable provider for a node
// type. When nodeType is blank the first provider is used, which lets the
// unified install endpoint default to the recommended engine.
func InstallableProviderByType(providers []InstallableProvider, nodeType string) (InstallableProvider, bool) {
	nodeType = strings.TrimSpace(nodeType)
	if nodeType == "" && len(providers) > 0 {
		return providers[0], true
	}
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
	exitSudoPassword         = 88
	exitPromptContractChange = 87
	exitMissingCLI           = 86
	exitNoDownloader         = 85
	// exitRemoteTimeout is GNU timeout's exit code. A provider stage that is
	// cut off remotely may still have succeeded (the upstream script tails
	// container logs on some paths), so only stages that opt in treat 124 as
	// non-fatal; the install readback is the authority on success.
	exitRemoteTimeout = 124
)

// sudoPreamble performs a non-interactive privilege check: already root, or
// passwordless sudo.  Exit 88 lets callers distinguish "sudo wants a
// password" from a real failure.
const sudoPreamble = `if [ "$(id -u)" -eq 0 ]; then SUDO=""; elif sudo -n true 2>/dev/null; then SUDO="sudo -n"; else echo "this action requires root or passwordless sudo" >&2; exit 88; fi; `

// downloadPreamble downloads $1 to a temp file in $SCRIPT, preferring curl and
// falling back to wget, exiting 85 when neither is present. It is shared by
// every provider install so the download contract (and its failure mode) is
// identical everywhere.
const downloadPreamble = `SCRIPT="$(mktemp "${TMPDIR:-/tmp}/nodexia-install.XXXXXX")" || exit 1; ` +
	`if command -v curl >/dev/null 2>&1; then curl -fsSL URL -o "$SCRIPT" || { echo "download failed" >&2; rm -f "$SCRIPT"; exit 85; }; ` +
	`elif command -v wget >/dev/null 2>&1; then wget -qO "$SCRIPT" URL || { echo "download failed" >&2; rm -f "$SCRIPT"; exit 85; }; ` +
	`else echo "curl or wget is required to install" >&2; rm -f "$SCRIPT"; exit 85; fi; `

// nodeNamePattern guards every node name interpolated into a shell command.
// The charset is intentionally strict: it covers real pg-node instance names
// ("node", "node2", "pg-node") while making injection impossible.
var nodeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// sanEntriesPattern validates the optional comma separated SAN list passed to
// the PasarGuard installer. Any character outside this set is rejected rather
// than escaped, because the value is interpolated into an outer sh -c '...'.
var sanEntriesPattern = regexp.MustCompile(`^[A-Za-z0-9.,*:_-]+$`)

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

// validPort returns the canonical port string if value is a port in 1..65535,
// otherwise "". isValidPort is the boolean form used by plan builders.
func validPort(value string) string {
	return parsePortFromEnv(map[string]string{"p": value}, "p")
}

func isValidPort(value string) bool { return validPort(value) != "" }

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
