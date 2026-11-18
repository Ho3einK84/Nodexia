package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	DriverSQLite = "sqlite"
	DriverMySQL  = "mysql"
)

// devSessionSecret is the insecure fallback secret applied only in development
// and test environments. It must never be accepted in production or staging.
const devSessionSecret = "nodexia-dev-session-secret-change-me"

// minProductionSessionSecretLength is the minimum length enforced for the
// session secret outside development/test, so signed cookies cannot be forged
// by brute-forcing a short key.
const minProductionSessionSecretLength = 16

// weakAdminPassword is the trivial default used in development and test.
// exampleAdminPassword is the .env.example placeholder.
// Neither may be used as the admin password outside development or test.
const (
	weakAdminPassword    = "admin"
	exampleAdminPassword = "change-this-password"
)

type Config struct {
	Version     string
	Environment string
	App         AppConfig
	Log         LogConfig
	HTTP        HTTPConfig
	SSH         SSHConfig
	Scheduler   SchedulerConfig
	Security    SecurityConfig
	Database    DatabaseConfig
	Install     InstallConfig
	Notify      NotifyConfig
	Digest      DigestConfig
}

type AppConfig struct {
	Name string
}

type LogConfig struct {
	Level  string
	Format string
}

type HTTPConfig struct {
	Address         string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

type SSHConfig struct {
	ConnectTimeout time.Duration
	CommandTimeout time.Duration
}

type SchedulerConfig struct {
	Enabled            bool
	StartupDelay       time.Duration
	SweepInterval      time.Duration
	MonitoringInterval time.Duration
	NodesInterval      time.Duration
	RetryBackoff       time.Duration
	ConnectTimeout     time.Duration
	CommandTimeout     time.Duration
}

type SecurityConfig struct {
	SessionCookieName   string
	SessionSecret       string
	SessionTTL          time.Duration
	SessionCookieSecure bool
	SSHHostKeyPolicy    string
	SSHKnownHostsPath   string
	AdminUsername       string
	AdminPassword       string
	// MetricsToken enables GET /metrics (Prometheus text format) when set:
	// scrapers must present it as a Bearer token or ?token=. Empty (the
	// default) keeps the endpoint disabled — it returns 404.
	MetricsToken string
}

type DatabaseConfig struct {
	Driver          string
	SQLitePath      string
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

type InstallConfig struct {
	Domain             string
	AutoTLS            bool
	EnvFile            string
	BehindReverseProxy bool
}

// NotifyConfig holds notification transport settings. The Telegram bot token is
// a secret: it lives only in the environment, is never persisted to the
// database, and must never be logged or rendered. It is optional — when empty,
// sending is disabled and the UI shows a "not configured" notice.
type NotifyConfig struct {
	TelegramBotToken string
}

// DigestConfig controls the periodic status digest sent to Telegram. It is
// DISABLED by default so existing deployments never start sending unexpectedly —
// an operator must opt in with NODEXIA_DIGEST_ENABLED=true. Channel selects which
// notification channel (by name, case-insensitive) receives the digest; an empty
// Channel falls back to every enabled channel, mirroring how a rule with no
// specific channel dispatches.
type DigestConfig struct {
	Enabled  bool
	Interval time.Duration
	Channel  string
}

func Load(version string) (Config, error) {
	envFile := envOrDefault("NODEXIA_ENV_FILE", ".env")
	if err := loadEnvFile(envFile); err != nil {
		return Config{}, err
	}

	cfg := Config{
		Version:     version,
		Environment: envOrDefault("NODEXIA_ENV", "development"),
		App: AppConfig{
			Name: envOrDefault("NODEXIA_APP_NAME", "Nodexia"),
		},
		Log: LogConfig{
			Level:  envOrDefault("NODEXIA_LOG_LEVEL", "info"),
			Format: envOrDefault("NODEXIA_LOG_FORMAT", "text"),
		},
		HTTP: HTTPConfig{
			Address:     envOrDefault("NODEXIA_HTTP_ADDR", ":8080"),
			ReadTimeout: durationFromEnv("NODEXIA_HTTP_READ_TIMEOUT", 15*time.Second),
			// The write timeout must comfortably exceed the slowest synchronous
			// handler (an SSH connection test bounded by the 10 s connect
			// timeout); long-running work (command runs, bulk actions) executes
			// in background jobs, never inside a request.
			WriteTimeout:    durationFromEnv("NODEXIA_HTTP_WRITE_TIMEOUT", 60*time.Second),
			IdleTimeout:     durationFromEnv("NODEXIA_HTTP_IDLE_TIMEOUT", 30*time.Second),
			ShutdownTimeout: durationFromEnv("NODEXIA_HTTP_SHUTDOWN_TIMEOUT", 10*time.Second),
		},
		SSH: SSHConfig{
			ConnectTimeout: durationFromEnv("NODEXIA_SSH_CONNECT_TIMEOUT", 10*time.Second),
			CommandTimeout: durationFromEnv("NODEXIA_SSH_COMMAND_TIMEOUT", 20*time.Second),
		},
		Scheduler: SchedulerConfig{
			Enabled:            boolFromEnv("NODEXIA_SCHEDULER_ENABLED", true),
			StartupDelay:       durationFromEnv("NODEXIA_SCHEDULER_STARTUP_DELAY", 15*time.Second),
			SweepInterval:      durationFromEnv("NODEXIA_SCHEDULER_SWEEP_INTERVAL", 1*time.Minute),
			MonitoringInterval: durationFromEnv("NODEXIA_SCHEDULER_MONITORING_INTERVAL", 15*time.Minute),
			NodesInterval:      durationFromEnv("NODEXIA_SCHEDULER_NODES_INTERVAL", 12*time.Hour),
			RetryBackoff:       durationFromEnv("NODEXIA_SCHEDULER_RETRY_BACKOFF", 3*time.Minute),
			ConnectTimeout:     durationFromEnv("NODEXIA_SCHEDULER_CONNECT_TIMEOUT", 10*time.Second),
			CommandTimeout:     durationFromEnv("NODEXIA_SCHEDULER_COMMAND_TIMEOUT", 45*time.Second),
		},
		Security: SecurityConfig{
			SessionCookieName:   envOrDefault("NODEXIA_SESSION_COOKIE_NAME", "nodexia_session"),
			SessionSecret:       strings.TrimSpace(os.Getenv("NODEXIA_SESSION_SECRET")),
			SessionTTL:          durationFromEnv("NODEXIA_SESSION_TTL", 12*time.Hour),
			SessionCookieSecure: boolFromEnv("NODEXIA_SESSION_COOKIE_SECURE", strings.EqualFold(envOrDefault("NODEXIA_ENV", "development"), "production")),
			SSHHostKeyPolicy:    strings.ToLower(envOrDefault("NODEXIA_SSH_HOST_KEY_POLICY", "tofu")),
			SSHKnownHostsPath:   envOrDefault("NODEXIA_SSH_KNOWN_HOSTS_PATH", filepath.Join("data", "ssh_known_hosts.json")),
			AdminUsername:       strings.TrimSpace(os.Getenv("NODEXIA_AUTH_USERNAME")),
			AdminPassword:       strings.TrimSpace(os.Getenv("NODEXIA_AUTH_PASSWORD")),
			MetricsToken:        strings.TrimSpace(os.Getenv("NODEXIA_METRICS_TOKEN")),
		},
		Database: DatabaseConfig{
			Driver:          strings.ToLower(envOrDefault("NODEXIA_DB_DRIVER", DriverSQLite)),
			SQLitePath:      envOrDefault("NODEXIA_DB_SQLITE_PATH", filepath.Join("data", "nodexia.sqlite3")),
			DSN:             os.Getenv("NODEXIA_DB_DSN"),
			MaxOpenConns:    intFromEnv("NODEXIA_DB_MAX_OPEN_CONNS", 10),
			MaxIdleConns:    intFromEnv("NODEXIA_DB_MAX_IDLE_CONNS", 5),
			ConnMaxLifetime: durationFromEnv("NODEXIA_DB_CONN_MAX_LIFETIME", 5*time.Minute),
		},
		Install: InstallConfig{
			Domain:             strings.TrimSpace(os.Getenv("NODEXIA_DOMAIN")),
			AutoTLS:            boolFromEnv("NODEXIA_AUTO_TLS", true),
			EnvFile:            envFile,
			BehindReverseProxy: boolFromEnv("NODEXIA_BEHIND_REVERSE_PROXY", false),
		},
		Notify: NotifyConfig{
			TelegramBotToken: strings.TrimSpace(os.Getenv("NODEXIA_TELEGRAM_BOT_TOKEN")),
		},
		Digest: DigestConfig{
			Enabled:  boolFromEnv("NODEXIA_DIGEST_ENABLED", false),
			Interval: durationFromEnv("NODEXIA_DIGEST_INTERVAL", 24*time.Hour),
			Channel:  strings.TrimSpace(os.Getenv("NODEXIA_DIGEST_CHANNEL")),
		},
	}

	if cfg.Environment == "development" || cfg.Environment == "test" {
		if strings.TrimSpace(cfg.Security.SessionSecret) == "" {
			cfg.Security.SessionSecret = devSessionSecret
		}
		if strings.TrimSpace(cfg.Security.AdminUsername) == "" {
			cfg.Security.AdminUsername = "admin"
		}
		if strings.TrimSpace(cfg.Security.AdminPassword) == "" {
			cfg.Security.AdminPassword = "admin"
		}
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.App.Name) == "" {
		return errors.New("config: NODEXIA_APP_NAME cannot be empty")
	}

	switch strings.ToLower(strings.TrimSpace(c.Log.Format)) {
	case "text", "json":
	default:
		return fmt.Errorf("config: NODEXIA_LOG_FORMAT must be %q or %q; got %q", "text", "json", c.Log.Format)
	}

	switch strings.ToLower(strings.TrimSpace(c.Log.Level)) {
	case "debug", "info", "warn", "warning", "error":
	default:
		return fmt.Errorf("config: NODEXIA_LOG_LEVEL must be debug, info, warn, or error; got %q", c.Log.Level)
	}

	switch c.Environment {
	case "development", "staging", "production", "test":
	default:
		return fmt.Errorf("config: NODEXIA_ENV must be one of development, staging, production, test; got %q", c.Environment)
	}

	if strings.TrimSpace(c.HTTP.Address) == "" {
		return errors.New("config: NODEXIA_HTTP_ADDR cannot be empty")
	}

	if c.HTTP.ReadTimeout <= 0 {
		return errors.New("config: NODEXIA_HTTP_READ_TIMEOUT must be greater than zero")
	}

	if c.HTTP.WriteTimeout <= 0 {
		return errors.New("config: NODEXIA_HTTP_WRITE_TIMEOUT must be greater than zero")
	}

	if c.HTTP.IdleTimeout <= 0 {
		return errors.New("config: NODEXIA_HTTP_IDLE_TIMEOUT must be greater than zero")
	}

	if c.HTTP.ShutdownTimeout <= 0 {
		return errors.New("config: NODEXIA_HTTP_SHUTDOWN_TIMEOUT must be greater than zero")
	}

	if c.SSH.ConnectTimeout <= 0 {
		return errors.New("config: NODEXIA_SSH_CONNECT_TIMEOUT must be greater than zero")
	}

	if c.SSH.CommandTimeout <= 0 {
		return errors.New("config: NODEXIA_SSH_COMMAND_TIMEOUT must be greater than zero")
	}

	if c.Scheduler.StartupDelay < 0 {
		return errors.New("config: NODEXIA_SCHEDULER_STARTUP_DELAY cannot be negative")
	}

	if c.Scheduler.SweepInterval <= 0 {
		return errors.New("config: NODEXIA_SCHEDULER_SWEEP_INTERVAL must be greater than zero")
	}

	if c.Scheduler.MonitoringInterval <= 0 {
		return errors.New("config: NODEXIA_SCHEDULER_MONITORING_INTERVAL must be greater than zero")
	}

	if c.Scheduler.NodesInterval <= 0 {
		return errors.New("config: NODEXIA_SCHEDULER_NODES_INTERVAL must be greater than zero")
	}

	if c.Scheduler.RetryBackoff <= 0 {
		return errors.New("config: NODEXIA_SCHEDULER_RETRY_BACKOFF must be greater than zero")
	}

	if c.Scheduler.ConnectTimeout <= 0 {
		return errors.New("config: NODEXIA_SCHEDULER_CONNECT_TIMEOUT must be greater than zero")
	}

	if c.Scheduler.CommandTimeout <= 0 {
		return errors.New("config: NODEXIA_SCHEDULER_COMMAND_TIMEOUT must be greater than zero")
	}

	if strings.TrimSpace(c.Security.SessionCookieName) == "" {
		return errors.New("config: NODEXIA_SESSION_COOKIE_NAME cannot be empty")
	}

	if c.Security.SessionTTL <= 0 {
		return errors.New("config: NODEXIA_SESSION_TTL must be greater than zero")
	}

	if strings.TrimSpace(c.Security.SSHKnownHostsPath) == "" {
		return errors.New("config: NODEXIA_SSH_KNOWN_HOSTS_PATH cannot be empty")
	}

	switch c.Security.SSHHostKeyPolicy {
	case "tofu", "insecure":
	default:
		return fmt.Errorf("config: NODEXIA_SSH_HOST_KEY_POLICY must be %q or %q; got %q", "tofu", "insecure", c.Security.SSHHostKeyPolicy)
	}

	if c.Environment == "production" || c.Environment == "staging" {
		secret := strings.TrimSpace(c.Security.SessionSecret)
		if secret == "" {
			return errors.New("config: NODEXIA_SESSION_SECRET cannot be empty outside development or test")
		}
		if secret == devSessionSecret {
			return errors.New("config: NODEXIA_SESSION_SECRET must not use the development default outside development or test")
		}
		if len(secret) < minProductionSessionSecretLength {
			return fmt.Errorf("config: NODEXIA_SESSION_SECRET must be at least %d characters outside development or test", minProductionSessionSecretLength)
		}

		if strings.TrimSpace(c.Security.AdminUsername) == "" {
			return errors.New("config: NODEXIA_AUTH_USERNAME cannot be empty outside development or test")
		}
		if strings.TrimSpace(c.Security.AdminPassword) == "" {
			return errors.New("config: NODEXIA_AUTH_PASSWORD cannot be empty outside development or test")
		}
		if pw := strings.TrimSpace(c.Security.AdminPassword); pw == weakAdminPassword || pw == exampleAdminPassword {
			return errors.New("config: NODEXIA_AUTH_PASSWORD must not use a known-weak password outside development or test")
		}
		if len(strings.TrimSpace(c.Security.AdminPassword)) < 8 {
			return errors.New("config: NODEXIA_AUTH_PASSWORD must be at least 8 characters outside development or test")
		}
	}

	if c.Digest.Enabled && c.Digest.Interval <= 0 {
		return errors.New("config: NODEXIA_DIGEST_INTERVAL must be greater than zero when the digest is enabled")
	}

	switch c.Database.Driver {
	case DriverSQLite:
		if strings.TrimSpace(c.Database.SQLitePath) == "" {
			return errors.New("config: NODEXIA_DB_SQLITE_PATH cannot be empty when sqlite is selected")
		}
	case DriverMySQL:
		if strings.TrimSpace(c.Database.DSN) == "" {
			return errors.New("config: NODEXIA_DB_DSN cannot be empty when mysql is selected")
		}
	default:
		return fmt.Errorf("config: NODEXIA_DB_DRIVER must be %q or %q; got %q", DriverSQLite, DriverMySQL, c.Database.Driver)
	}

	if c.Database.MaxOpenConns < 1 {
		return errors.New("config: NODEXIA_DB_MAX_OPEN_CONNS must be at least 1")
	}

	if c.Database.MaxIdleConns < 0 {
		return errors.New("config: NODEXIA_DB_MAX_IDLE_CONNS cannot be negative")
	}

	if c.Database.ConnMaxLifetime < 0 {
		return errors.New("config: NODEXIA_DB_CONN_MAX_LIFETIME cannot be negative")
	}

	return nil
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	return value
}

func durationFromEnv(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	if parsed, err := time.ParseDuration(value); err == nil {
		return parsed
	}

	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second
	}

	return fallback
}

func intFromEnv(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}

func boolFromEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}

	return parsed
}
