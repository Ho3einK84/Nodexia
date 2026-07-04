package servers

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

const (
	AuthModeKey      = "key"
	AuthModePassword = "password"
	AuthModeHybrid   = "hybrid"

	// CredentialStrategyRuntime: password is entered in the browser each time.
	// If credential_ref is also filled in, that value doubles as a scheduler
	// password (backward-compatible behaviour).
	CredentialStrategyRuntime = "runtime"

	// CredentialStrategyStored: the SSH password is stored directly in
	// credential_ref. The scheduler can use it for background jobs.
	CredentialStrategyStored = "stored"

	// CredentialStrategyExternalRef: a key=value map (env-var names, file
	// paths) is stored in credential_ref and resolved at connection time.
	CredentialStrategyExternalRef = "external_ref"

	// CredentialStrategyAgentReady: no credentials stored; connections use
	// the SSH agent socket (SSH_AUTH_SOCK).
	CredentialStrategyAgentReady = "agent_ready"
)

type ValidationErrors map[string]string

func (v ValidationErrors) Add(field string, message string) {
	if _, exists := v[field]; exists {
		return
	}

	v[field] = message
}

func (v ValidationErrors) HasAny() bool {
	return len(v) > 0
}

type FormInput struct {
	Name               string
	Host               string
	Port               string
	AuthMode           string
	Username           string
	Tags               string
	Note               string
	CredentialStrategy string
	CredentialRef      string
	TrafficResetDay    string
}

type ValidatedForm struct {
	Server Server
	Input  FormInput
}

func ValidateForm(input FormInput) (ValidatedForm, ValidationErrors) {
	errors := ValidationErrors{}

	host := strings.TrimSpace(input.Host)
	username := strings.TrimSpace(input.Username)

	if idx := strings.LastIndexByte(host, '@'); idx >= 0 {
		parsedUser := host[:idx]
		host = host[idx+1:]
		if username == "" {
			username = parsedUser
		}
	}

	server := Server{
		Name:               strings.TrimSpace(input.Name),
		Host:               host,
		AuthMode:           strings.TrimSpace(input.AuthMode),
		Username:           username,
		Note:               strings.TrimSpace(input.Note),
		CredentialStrategy: strings.TrimSpace(input.CredentialStrategy),
		CredentialRef:      strings.TrimSpace(input.CredentialRef),
	}

	port := parsePort(strings.TrimSpace(input.Port), errors)
	server.Port = port

	server.TrafficResetDay = parseTrafficResetDay(strings.TrimSpace(input.TrafficResetDay), errors)

	validateName(server.Name, errors)
	validateHost(server.Host, errors)
	validateAuthMode(server.AuthMode, errors)
	validateUsername(server.Username, errors)
	validateCredential(server.CredentialStrategy, server.CredentialRef, errors)

	tags, tagErr := normalizeTags(input.Tags)
	if tagErr != nil {
		errors.Add("tags", tagErr.Error())
	}
	server.Tags = tags

	return ValidatedForm{
		Server: server,
		Input: FormInput{
			Name:               server.Name,
			Host:               host,
			Port:               strconv.Itoa(server.Port),
			AuthMode:           server.AuthMode,
			Username:           server.Username,
			Tags:               strings.Join(server.Tags, ", "),
			Note:               server.Note,
			CredentialStrategy: server.CredentialStrategy,
			CredentialRef:      server.CredentialRef,
			TrafficResetDay:    strconv.Itoa(server.TrafficResetDay),
		},
	}, errors
}

func DefaultFormInput() FormInput {
	return FormInput{
		Port:               "22",
		AuthMode:           AuthModeHybrid,
		CredentialStrategy: CredentialStrategyStored,
		TrafficResetDay:    "1",
	}
}

func FormInputFromServer(server Server) FormInput {
	resetDay := server.TrafficResetDay
	if resetDay < 1 || resetDay > 28 {
		resetDay = 1
	}
	return FormInput{
		Name:               server.Name,
		Host:               server.Host,
		Port:               strconv.Itoa(server.Port),
		AuthMode:           server.AuthMode,
		Username:           server.Username,
		Tags:               strings.Join(server.Tags, ", "),
		Note:               server.Note,
		CredentialStrategy: server.CredentialStrategy,
		CredentialRef:      server.CredentialRef,
		TrafficResetDay:    strconv.Itoa(resetDay),
	}
}

func parsePort(value string, errors ValidationErrors) int {
	if value == "" {
		return 22
	}

	port, err := strconv.Atoi(value)
	if err != nil {
		errors.Add("port", "Port must be a valid integer.")
		return 22
	}

	if port < 1 || port > 65535 {
		errors.Add("port", "Port must be between 1 and 65535.")
		return 22
	}

	return port
}

// parseTrafficResetDay validates the billing-cycle anchor day. Empty means the
// default (1 = calendar month). Days 29-31 are rejected so the period start
// exists in every month.
func parseTrafficResetDay(value string, errors ValidationErrors) int {
	if value == "" {
		return 1
	}

	day, err := strconv.Atoi(value)
	if err != nil || day < 1 || day > 28 {
		errors.Add("traffic_reset_day", "Traffic reset day must be a number between 1 and 28.")
		return 1
	}

	return day
}

func validateName(value string, errors ValidationErrors) {
	if value == "" {
		errors.Add("name", "Server name is required.")
		return
	}

	if len(value) > 120 {
		errors.Add("name", "Server name must be 120 characters or fewer.")
	}
}

func validateHost(value string, errors ValidationErrors) {
	if value == "" {
		errors.Add("host", "Host is required.")
		return
	}

	if strings.Contains(value, "://") {
		errors.Add("host", "Host must not include a URL scheme.")
		return
	}

	if strings.ContainsAny(value, "/\\") {
		errors.Add("host", "Host must be a hostname or IP address only.")
		return
	}

	if strings.Contains(value, " ") {
		errors.Add("host", "Host must not contain spaces.")
		return
	}

	host := value
	if idx := strings.LastIndexByte(value, '@'); idx >= 0 {
		host = value[idx+1:]
	}

	if ip := net.ParseIP(host); ip != nil {
		return
	}

	if _, err := url.Parse("//" + host); err != nil {
		errors.Add("host", fmt.Sprintf("Host is invalid: %v", err))
	}
}

func validateAuthMode(value string, errors ValidationErrors) {
	switch value {
	case AuthModeKey, AuthModePassword, AuthModeHybrid:
		return
	default:
		errors.Add("auth_mode", "Auth mode must be key, password, or hybrid.")
	}
}

func validateUsername(value string, errors ValidationErrors) {
	if value == "" {
		errors.Add("username", "Username is required.")
		return
	}

	if len(value) > 64 {
		errors.Add("username", "Username must be 64 characters or fewer.")
	}
}

func validateCredential(strategy string, reference string, errors ValidationErrors) {
	switch strategy {
	case CredentialStrategyRuntime, CredentialStrategyStored,
		CredentialStrategyExternalRef, CredentialStrategyAgentReady:
	default:
		errors.Add("credential_strategy", "Credential strategy must be stored, runtime, external_ref, or agent_ready.")
		return
	}

	switch strategy {
	case CredentialStrategyStored:
		// A literal SSH password — any printable character is fine; cap at a
		// generous length that still fits comfortably in a TEXT column.
		if len(reference) > 1024 {
			errors.Add("credential_ref", "Stored credential must be 1024 characters or fewer.")
		}
	case CredentialStrategyExternalRef:
		if reference == "" {
			errors.Add("credential_ref", "Credential reference is required when using external_ref.")
		}
		if len(reference) > 512 {
			errors.Add("credential_ref", "Credential reference must be 512 characters or fewer.")
		}
		if reference != "" {
			if err := validateCredentialReference(reference); err != nil {
				errors.Add("credential_ref", err.Error())
			}
		}
	default:
		// runtime, agent_ready: no constraint on reference value length/format.
		if len(reference) > 1024 {
			errors.Add("credential_ref", "Credential must be 1024 characters or fewer.")
		}
	}
}

func normalizeTags(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	tags := make([]string, 0, len(parts))

	for _, part := range parts {
		tag := strings.TrimSpace(part)
		if tag == "" {
			continue
		}

		if len(tag) > 40 {
			return nil, fmt.Errorf("Each label must be 40 characters or fewer")
		}

		normalized := strings.ToLower(tag)
		if _, exists := seen[normalized]; exists {
			continue
		}

		seen[normalized] = struct{}{}
		tags = append(tags, normalized)
	}

	return tags, nil
}

func validateCredentialReference(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	if strings.Contains(value, "://") {
		return nil
	}

	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ';' || r == '\n' || r == '\r'
	})
	if len(parts) == 0 {
		return fmt.Errorf("Credential reference must use key=value segments or a secret-manager URI")
	}

	allowedKeys := map[string]struct{}{
		"password_env":      {},
		"password_file":     {},
		"key_env":           {},
		"key_file":          {},
		"passphrase_env":    {},
		"passphrase_file":   {},
		"traffic_interface": {},
	}

	for _, part := range parts {
		key, current, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return fmt.Errorf("Credential reference segment %q must use key=value", part)
		}
		key = strings.TrimSpace(strings.ToLower(key))
		current = strings.TrimSpace(current)
		if _, ok := allowedKeys[key]; !ok {
			return fmt.Errorf("Credential reference key %q is not supported", key)
		}
		if current == "" {
			return fmt.Errorf("Credential reference key %q must include a non-empty value", key)
		}
		if !isSafeReferenceValue(current) {
			return fmt.Errorf("Credential reference value for %q contains unsupported characters", key)
		}
	}

	return nil
}

func isSafeReferenceValue(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		switch r {
		case '-', '_', '.', '/', ':', '@':
			continue
		default:
			return false
		}
	}
	return true
}
