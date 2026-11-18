package testutil

import (
	"path/filepath"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/config"
)

// TestConfig returns a validated test configuration with the scheduler disabled.
func TestConfig(t *testing.T) config.Config {
	t.Helper()
	ClearNodexiaEnv(t)

	missingEnv := filepath.Join(t.TempDir(), "missing.env")
	t.Setenv("NODEXIA_ENV_FILE", missingEnv)
	t.Setenv("NODEXIA_ENV", "test")
	t.Setenv("NODEXIA_SCHEDULER_ENABLED", "false")
	t.Setenv("NODEXIA_DB_SQLITE_PATH", filepath.Join(t.TempDir(), "nodexia.test.sqlite3"))
	t.Setenv("NODEXIA_SSH_KNOWN_HOSTS_PATH", filepath.Join(t.TempDir(), "ssh_known_hosts.json"))

	cfg, err := config.Load("v0.1.0-test")
	if err != nil {
		t.Fatalf("load test config: %v", err)
	}

	return cfg
}
