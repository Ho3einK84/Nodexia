package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
)

func TestLoadTestEnvironmentDefaults(t *testing.T) {
	testutil.ClearNodexiaEnv(t)
	t.Setenv("NODEXIA_ENV_FILE", filepath.Join(t.TempDir(), "missing.env"))
	t.Setenv("NODEXIA_ENV", "test")

	cfg, err := config.Load("v0.1.0")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Version != "v0.1.0" {
		t.Fatalf("Version = %q, want v0.1.0", cfg.Version)
	}
	if cfg.Environment != "test" {
		t.Fatalf("Environment = %q, want test", cfg.Environment)
	}
	if cfg.Database.Driver != config.DriverSQLite {
		t.Fatalf("Database.Driver = %q, want sqlite", cfg.Database.Driver)
	}
	if cfg.Security.SessionSecret == "" {
		t.Fatal("expected development/test session secret fallback")
	}
}

func TestLoadDigestDefaultsDisabled(t *testing.T) {
	testutil.ClearNodexiaEnv(t)
	t.Setenv("NODEXIA_ENV_FILE", filepath.Join(t.TempDir(), "missing.env"))
	t.Setenv("NODEXIA_ENV", "test")

	cfg, err := config.Load("v0.1.0")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Digest.Enabled {
		t.Fatal("digest must be disabled by default")
	}
	if cfg.Digest.Interval <= 0 {
		t.Fatalf("digest interval default = %v, want > 0", cfg.Digest.Interval)
	}
	if cfg.Digest.Channel != "" {
		t.Fatalf("digest channel default = %q, want empty", cfg.Digest.Channel)
	}
}

func TestLoadRejectsDigestEnabledWithoutInterval(t *testing.T) {
	testutil.ClearNodexiaEnv(t)
	t.Setenv("NODEXIA_ENV_FILE", filepath.Join(t.TempDir(), "missing.env"))
	t.Setenv("NODEXIA_ENV", "test")
	t.Setenv("NODEXIA_DIGEST_ENABLED", "true")
	t.Setenv("NODEXIA_DIGEST_INTERVAL", "0s")

	if _, err := config.Load("v0.1.0"); err == nil {
		t.Fatal("expected error when the digest is enabled with a non-positive interval")
	}
}

func TestLoadRejectsInvalidLogFormat(t *testing.T) {
	testutil.ClearNodexiaEnv(t)
	t.Setenv("NODEXIA_ENV", "test")
	t.Setenv("NODEXIA_LOG_FORMAT", "yaml")

	_, err := config.Load("dev")
	if err == nil {
		t.Fatal("expected validation error for invalid log format")
	}
}

func TestLoadRequiresProductionSessionSecret(t *testing.T) {
	testutil.ClearNodexiaEnv(t)
	t.Setenv("NODEXIA_ENV", "production")
	t.Setenv("NODEXIA_SESSION_SECRET", "")

	_, err := config.Load("v0.1.0")
	if err == nil {
		t.Fatal("expected validation error for missing production session secret")
	}
}

func TestLoadRejectsDevDefaultSecretInProduction(t *testing.T) {
	testutil.ClearNodexiaEnv(t)
	t.Setenv("NODEXIA_ENV", "production")
	t.Setenv("NODEXIA_SESSION_SECRET", "nodexia-dev-session-secret-change-me")
	t.Setenv("NODEXIA_AUTH_USERNAME", "admin")
	t.Setenv("NODEXIA_AUTH_PASSWORD", "a-strong-password")

	if _, err := config.Load("v0.1.0"); err == nil {
		t.Fatal("expected validation error for development default secret in production")
	}
}

func TestLoadRejectsShortSecretInProduction(t *testing.T) {
	testutil.ClearNodexiaEnv(t)
	t.Setenv("NODEXIA_ENV", "production")
	t.Setenv("NODEXIA_SESSION_SECRET", "tooshort")
	t.Setenv("NODEXIA_AUTH_USERNAME", "admin")
	t.Setenv("NODEXIA_AUTH_PASSWORD", "a-strong-password")

	if _, err := config.Load("v0.1.0"); err == nil {
		t.Fatal("expected validation error for short production session secret")
	}
}

func TestLoadRequiresProductionAdminCredentials(t *testing.T) {
	testutil.ClearNodexiaEnv(t)
	t.Setenv("NODEXIA_ENV", "production")
	t.Setenv("NODEXIA_SESSION_SECRET", "a-sufficiently-long-secret-value")

	if _, err := config.Load("v0.1.0"); err == nil {
		t.Fatal("expected validation error for missing production admin credentials")
	}
}

func TestLoadAcceptsValidProductionConfig(t *testing.T) {
	testutil.ClearNodexiaEnv(t)
	t.Setenv("NODEXIA_ENV", "production")
	t.Setenv("NODEXIA_SESSION_SECRET", "a-sufficiently-long-secret-value")
	t.Setenv("NODEXIA_AUTH_USERNAME", "admin")
	t.Setenv("NODEXIA_AUTH_PASSWORD", "a-strong-password")

	cfg, err := config.Load("v0.1.0")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Security.SessionCookieSecure {
		t.Fatal("production should default the session cookie to Secure")
	}
}

func TestAdminPasswordWeakness(t *testing.T) {
	strongPassword := "hunter2-but-longer-and-unique"
	strongSecret := "a-sufficiently-long-secret-value"

	tests := []struct {
		name     string
		env      string
		password string
		wantErr  bool
	}{
		{
			name:     "weak 'admin' rejected in production",
			env:      "production",
			password: "admin",
			wantErr:  true,
		},
		{
			name:     "example placeholder rejected in production",
			env:      "production",
			password: "change-this-password",
			wantErr:  true,
		},
		{
			name:     "weak 'admin' accepted in development",
			env:      "development",
			password: "admin",
			wantErr:  false,
		},
		{
			name:     "strong password accepted in production",
			env:      "production",
			password: strongPassword,
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.ClearNodexiaEnv(t)
			t.Setenv("NODEXIA_ENV_FILE", filepath.Join(t.TempDir(), "missing.env"))
			t.Setenv("NODEXIA_ENV", tt.env)
			t.Setenv("NODEXIA_AUTH_PASSWORD", tt.password)
			if tt.env == "production" || tt.env == "staging" {
				t.Setenv("NODEXIA_SESSION_SECRET", strongSecret)
				t.Setenv("NODEXIA_AUTH_USERNAME", "operator")
			}

			_, err := config.Load("v0.1.0")
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadReadsEnvFileOverrides(t *testing.T) {
	testutil.ClearNodexiaEnv(t)
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("NODEXIA_APP_NAME=FromFile\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	t.Setenv("NODEXIA_ENV_FILE", envFile)
	t.Setenv("NODEXIA_ENV", "test")

	cfg, err := config.Load("dev")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.App.Name != "FromFile" {
		t.Fatalf("App.Name = %q, want FromFile", cfg.App.Name)
	}
}
