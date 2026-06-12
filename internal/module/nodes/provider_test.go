package nodes

import (
	"testing"
	"time"
)

func TestValidateNodeName(t *testing.T) {
	valid := []string{"node", "node2", "pg-node", "my.node_3", "N1"}
	for _, name := range valid {
		if err := ValidateNodeName(name); err != nil {
			t.Errorf("ValidateNodeName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{"", " ", "node name", "node;reboot", "$(id)", "node`x`", "-node", "node/../etc", "node\nnode"}
	for _, name := range invalid {
		if err := ValidateNodeName(name); err == nil {
			t.Errorf("ValidateNodeName(%q) = nil, want error", name)
		}
	}
}

func TestProviderByType(t *testing.T) {
	providers := DefaultProviders()

	provider, ok := ProviderByType(providers, "pasarguard-node")
	if !ok || provider.DisplayName() != "PasarGuard" {
		t.Fatalf("ProviderByType(pasarguard-node) = %v, %v", provider, ok)
	}
	if !provider.SupportsInstall() {
		t.Errorf("PasarGuard provider must support installation")
	}

	provider, ok = ProviderByType(providers, "rebecca-node")
	if !ok || provider.DisplayName() != "Rebecca" {
		t.Fatalf("ProviderByType(rebecca-node) = %v, %v", provider, ok)
	}
	if provider.SupportsInstall() {
		t.Errorf("Rebecca provider must not support installation")
	}

	if _, ok := ProviderByType(providers, "unknown"); ok {
		t.Fatalf("ProviderByType(unknown) must report false")
	}
}

func TestParseEnvFile(t *testing.T) {
	env := parseEnvFile([]string{
		"# comment",
		"",
		`SERVICE_PORT = "62050"`,
		"API_KEY='abc'",
		"NOT A PAIR",
	})
	if env["SERVICE_PORT"] != "62050" {
		t.Errorf("SERVICE_PORT = %q", env["SERVICE_PORT"])
	}
	if env["API_KEY"] != "abc" {
		t.Errorf("API_KEY = %q", env["API_KEY"])
	}
	if _, found := env["NOT A PAIR"]; found {
		t.Errorf("malformed line must be skipped")
	}
}

func TestParsePortFromEnv(t *testing.T) {
	env := map[string]string{"OK": "62050", "ZERO": "0", "BIG": "70000", "TEXT": "abc"}
	if got := parsePortFromEnv(env, "OK"); got != "62050" {
		t.Errorf("OK = %q", got)
	}
	for _, key := range []string{"ZERO", "BIG", "TEXT", "MISSING"} {
		if got := parsePortFromEnv(env, key); got != "" {
			t.Errorf("%s = %q, want empty", key, got)
		}
	}
}

func TestExtractImageTag(t *testing.T) {
	if got := extractImageTag("pasarguard/node:latest"); got != "latest" {
		t.Errorf("tag = %q, want latest", got)
	}
	if got := extractImageTag("pasarguard/node"); got != "" {
		t.Errorf("tag = %q, want empty for untagged image", got)
	}
}

func TestDedupeSnapshots(t *testing.T) {
	now := time.Now().UTC()
	snapshots := dedupeSnapshots([]Snapshot{
		{NodeType: rebeccaType, ServiceName: "rebecca-node", CollectedAt: now},
		{NodeType: pasarguardType, ServiceName: "node", CollectedAt: now},
		{NodeType: pasarguardType, ServiceName: "Node", CollectedAt: now}, // duplicate, case-insensitive
		{NodeType: pasarguardType, ServiceName: "node2", CollectedAt: now},
	})

	if len(snapshots) != 3 {
		t.Fatalf("len(snapshots) = %d, want 3", len(snapshots))
	}
	// PasarGuard sorts before Rebecca; names sort within a type.
	if snapshots[0].ServiceName != "node" || snapshots[1].ServiceName != "node2" || snapshots[2].ServiceName != "rebecca-node" {
		t.Fatalf("unexpected order: %s, %s, %s", snapshots[0].ServiceName, snapshots[1].ServiceName, snapshots[2].ServiceName)
	}
}
