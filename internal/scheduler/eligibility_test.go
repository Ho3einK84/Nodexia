package scheduler

import (
	"strings"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/module/servers"
)

func TestEvaluateEligibility(t *testing.T) {
	r := &Runtime{}

	tests := []struct {
		name               string
		server             servers.Server
		wantAllowed        bool
		wantReasonContains string
	}{
		// ── stored strategy ──────────────────────────────────────────────
		{
			name:               "stored with password allowed",
			server:             servers.Server{CredentialStrategy: servers.CredentialStrategyStored, CredentialRef: "secret123"},
			wantAllowed:        true,
			wantReasonContains: "Stored password",
		},
		{
			name:               "stored without password blocked",
			server:             servers.Server{CredentialStrategy: servers.CredentialStrategyStored, CredentialRef: ""},
			wantAllowed:        false,
			wantReasonContains: "No password stored",
		},
		{
			name:               "stored with whitespace-only credential blocked",
			server:             servers.Server{CredentialStrategy: servers.CredentialStrategyStored, CredentialRef: "   "},
			wantAllowed:        false,
			wantReasonContains: "No password stored",
		},

		// ── runtime strategy (backward compat) ───────────────────────────
		{
			name:               "runtime with stored password allowed",
			server:             servers.Server{CredentialStrategy: servers.CredentialStrategyRuntime, CredentialRef: "mypass"},
			wantAllowed:        true,
			wantReasonContains: "Stored password available",
		},
		{
			name:               "runtime without password blocked",
			server:             servers.Server{CredentialStrategy: servers.CredentialStrategyRuntime, CredentialRef: ""},
			wantAllowed:        false,
			wantReasonContains: "No password stored",
		},

		// ── agent_ready ──────────────────────────────────────────────────
		{
			name:               "agent_ready blocked when SSH_AUTH_SOCK unset",
			server:             servers.Server{CredentialStrategy: servers.CredentialStrategyAgentReady},
			wantAllowed:        false,
			wantReasonContains: "SSH_AUTH_SOCK",
		},

		// ── external_ref ─────────────────────────────────────────────────
		{
			name:               "external_ref blocked when ref is empty",
			server:             servers.Server{CredentialStrategy: servers.CredentialStrategyExternalRef, CredentialRef: ""},
			wantAllowed:        false,
			wantReasonContains: "empty",
		},
		{
			name:               "external_ref with valid ref allowed",
			server:             servers.Server{CredentialStrategy: servers.CredentialStrategyExternalRef, AuthMode: "password", CredentialRef: "password_env=MY_SSH_PASS"},
			wantAllowed:        true,
			wantReasonContains: "External credential reference",
		},

		// ── unknown ──────────────────────────────────────────────────────
		{
			name:               "unknown strategy is blocked",
			server:             servers.Server{CredentialStrategy: "unknown_strategy"},
			wantAllowed:        false,
			wantReasonContains: "Unsupported",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "agent_ready blocked when SSH_AUTH_SOCK unset" {
				t.Setenv("SSH_AUTH_SOCK", "")
			}
			got := r.evaluateEligibility(tc.server)
			if got.Allowed != tc.wantAllowed {
				t.Errorf("Allowed = %v, want %v (reason: %q)", got.Allowed, tc.wantAllowed, got.Reason)
			}
			if tc.wantReasonContains != "" && !strings.Contains(got.Reason, tc.wantReasonContains) {
				t.Errorf("Reason = %q, want to contain %q", got.Reason, tc.wantReasonContains)
			}
		})
	}
}

func TestEvaluateEligibility_AgentReady_WithSocket(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "/tmp/ssh-agent.sock")

	r := &Runtime{}
	server := servers.Server{CredentialStrategy: servers.CredentialStrategyAgentReady}
	got := r.evaluateEligibility(server)

	if !got.Allowed {
		t.Errorf("agent_ready should be allowed when SSH_AUTH_SOCK is set, reason: %q", got.Reason)
	}
}
