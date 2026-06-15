package servers

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/i18n"
)

func formatTimestamp(value time.Time) string {
	if value.IsZero() {
		return "-"
	}

	return value.UTC().Format("2006-01-02 15:04:05 UTC")
}

func flashKind(r *http.Request) string {
	switch r.URL.Query().Get("flash") {
	case "created", "updated", "deleted", "host-key-forgotten":
		return "success"
	case "bulk-no-selection", "bulk-invalid-action", "bulk-job-expired":
		return "error"
	default:
		return ""
	}
}

func flashMessage(r *http.Request) string {
	key := ""
	switch r.URL.Query().Get("flash") {
	case "created":
		key = "servers.flash.created"
	case "updated":
		key = "servers.flash.updated"
	case "deleted":
		key = "servers.flash.deleted"
	case "host-key-forgotten":
		key = "servers.flash.host_key_forgotten"
	case "bulk-no-selection":
		key = "servers.flash.bulk_no_selection"
	case "bulk-invalid-action":
		key = "servers.flash.bulk_invalid_action"
	case "bulk-job-expired":
		key = "servers.flash.bulk_job_expired"
	default:
		return ""
	}
	return localizerFromRequest(r).T(key)
}

// localizerFromRequest returns the request's active localizer, falling back to
// the default-language localizer so flash/error helpers never panic.
func localizerFromRequest(r *http.Request) *i18n.Localizer {
	if loc := i18n.FromContext(r.Context()); loc != nil {
		return loc
	}
	return i18n.MustDefault().Localizer(i18n.DefaultLanguage)
}

func HasStoredCredentials(server Server) bool {
	switch server.CredentialStrategy {
	case CredentialStrategyStored:
		return strings.TrimSpace(server.CredentialRef) != ""
	case CredentialStrategyRuntime:
		// Backward compat: runtime servers that have a saved password in
		// credential_ref are treated as having stored credentials.
		return strings.TrimSpace(server.CredentialRef) != ""
	case CredentialStrategyExternalRef, CredentialStrategyAgentReady:
		return true
	default:
		return false
	}
}

func ResolveCredentials(server Server) (password, privateKey, keyPassphrase string) {
	switch server.CredentialStrategy {
	case CredentialStrategyStored:
		return strings.TrimSpace(server.CredentialRef), "", ""
	case CredentialStrategyRuntime:
		return strings.TrimSpace(server.CredentialRef), "", ""
	}

	if server.CredentialStrategy != CredentialStrategyExternalRef {
		return "", "", ""
	}

	ref := strings.TrimSpace(server.CredentialRef)
	if ref == "" {
		return "", "", ""
	}

	parts := strings.FieldsFunc(ref, func(r rune) bool {
		return r == ';' || r == '\n' || r == '\r'
	})

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}

		key = strings.TrimSpace(strings.ToLower(key))
		value = strings.TrimSpace(value)

		switch key {
		case "password_env":
			if password == "" {
				password = os.Getenv(value)
			}
		case "password_file":
			if password == "" {
				if data, err := os.ReadFile(value); err == nil {
					password = strings.TrimSpace(string(data))
				}
			}
		case "key_env":
			if privateKey == "" {
				privateKey = os.Getenv(value)
			}
		case "key_file":
			if privateKey == "" {
				if data, err := os.ReadFile(value); err == nil {
					privateKey = string(data)
				}
			}
		case "passphrase_env":
			if keyPassphrase == "" {
				keyPassphrase = os.Getenv(value)
			}
		case "passphrase_file":
			if keyPassphrase == "" {
				if data, err := os.ReadFile(value); err == nil {
					keyPassphrase = strings.TrimSpace(string(data))
				}
			}
		}
	}

	return password, privateKey, keyPassphrase
}
