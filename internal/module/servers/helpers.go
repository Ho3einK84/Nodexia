package servers

import (
	"net/http"
	"os"
	"strings"
	"time"
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
	default:
		return ""
	}
}

func flashMessage(r *http.Request) string {
	switch r.URL.Query().Get("flash") {
	case "created":
		return "Server record created successfully."
	case "updated":
		return "Server record updated successfully."
	case "deleted":
		return "Server record deleted successfully."
	case "host-key-forgotten":
		return "Stored host key removed. The next connection will trust and pin the new key."
	default:
		return ""
	}
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
