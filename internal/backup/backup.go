// Package backup implements the panel's logical export/restore feature. A
// backup is a single, versioned JSON document containing the panel's
// configuration/state rows (servers, alert rules, channels, silences, …) — not
// the regenerable telemetry the collectors rebuild on their own.
//
// Why a logical JSON export instead of copying the SQLite file: the panel must
// support both SQLite and MySQL, and a raw file copy is SQLite-only and opaque.
// A row→JSON dump is driver-neutral (it restores into either backend), is
// human-inspectable (so the secret-free guarantee can actually be verified by
// eye), and lets us redact individual secret-bearing fields rather than ship an
// all-or-nothing blob.
//
// Secrets policy: the only secret-bearing database column is
// servers.credential_ref, which holds a *plaintext* SSH password for the
// "stored"/"runtime" strategies (the panel does not encrypt it at rest) and
// env/file *references* for "external_ref". A default export therefore redacts
// credential_ref on every row and embeds no environment values, producing a
// secret-free file. The caller may opt in to a sensitive export that keeps
// credential_ref verbatim and embeds selected environment secrets; that file
// should be passphrase-encrypted (see crypto.go).
//
// Restore semantics are replace, not merge: the configuration tables are
// truncated and reloaded from the backup inside one transaction, preserving the
// original primary keys so cross-table references stay valid. A failed import
// rolls back and leaves the panel untouched.
package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/db"
	"github.com/Ho3einK84/Nodexia/internal/module/alerts"
	"github.com/Ho3einK84/Nodexia/internal/module/analytics"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
)

// FormatPlain identifies an unencrypted backup document; FormatEncrypted
// identifies the passphrase-encrypted envelope (see crypto.go).
const (
	FormatPlain     = "nodexia-backup"
	FormatEncrypted = "nodexia-backup-encrypted"

	// MaxArchiveBytes caps the size of a serialized backup accepted by Inspect
	// and HTTP restore handlers.
	MaxArchiveBytes = 8 << 20 // 8 MiB
)

// SchemaVersion is the backup document version. Bump it when the on-disk shape
// changes; Inspect refuses documents from a newer version so a future format is
// never silently half-restored by an older binary.
const SchemaVersion = 1

// Sentinel errors let the HTTP layer map a failure to a specific localized
// message without string matching.
var (
	ErrBadFormat          = errors.New("backup: unrecognised file format")
	ErrUnsupportedVersion = errors.New("backup: unsupported backup version")
	ErrPassphraseRequired = errors.New("backup: file is encrypted, passphrase required")
	ErrWrongPassphrase    = errors.New("backup: incorrect passphrase or corrupt file")
	ErrMalformed          = errors.New("backup: malformed backup content")
	ErrDanglingReference  = errors.New("backup: backup references a missing server or channel")
	ErrTooLarge           = errors.New("backup: backup exceeds maximum allowed size")
)

// Archive is the decoded, plaintext backup document.
type Archive struct {
	Format          string `json:"format"`
	Version         int    `json:"version"`
	CreatedAt       string `json:"created_at"`
	AppVersion      string `json:"app_version"`
	SourceDriver    string `json:"source_driver"`
	IncludesSecrets bool   `json:"includes_secrets"`
	Data            Data   `json:"data"`
	// Env carries selected NODEXIA_* secrets and is only populated when the
	// export opted into secrets. It is never applied automatically on restore
	// (environment belongs in the host's env/.env); it travels with the archive
	// purely so a migration can be a single file.
	Env map[string]string `json:"env,omitempty"`
}

// Data holds the exported rows, grouped by table. Only configuration/state
// tables are included; telemetry the collectors rebuild is deliberately omitted.
type Data struct {
	Servers             []ServerRow             `json:"servers"`
	ServerTags          []ServerTagRow          `json:"server_tags"`
	ServerTrafficLimits []ServerTrafficLimitRow `json:"server_traffic_limits,omitempty"`
	TrafficLimitRules   []TrafficLimitRuleRow   `json:"traffic_limit_rules,omitempty"`
	AlertChannels       []AlertChannelRow       `json:"alert_channels"`
	AlertRules          []AlertRuleRow          `json:"alert_rules"`
	AlertSilences       []AlertSilenceRow       `json:"alert_silences"`
	InstallMetadata     []InstallMetaRow        `json:"install_metadata"`
}

// ServerRow mirrors the secret-bearing servers table. CredentialRef is redacted
// (empty) unless the export opted into secrets.
type ServerRow struct {
	ID                 int64   `json:"id"`
	Name               string  `json:"name"`
	Host               string  `json:"host"`
	Port               int     `json:"port"`
	AuthMode           string  `json:"auth_mode"`
	Username           string  `json:"username"`
	Note               string  `json:"note"`
	CredentialStrategy string  `json:"credential_strategy"`
	CredentialRef      string  `json:"credential_ref"`
	CountryCode        string  `json:"country_code"`
	CountryName        string  `json:"country_name"`
	CountryCheckedAt   *string `json:"country_checked_at,omitempty"`
	TrafficResetDay    int     `json:"traffic_reset_day,omitempty"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
}

// ServerTrafficLimitRow mirrors the server_traffic_limits table.
type ServerTrafficLimitRow struct {
	ServerID          int64  `json:"server_id"`
	MonthlyLimitBytes int64  `json:"monthly_limit_bytes"`
	LimitKind         string `json:"limit_kind"`
	UpdatedAt         string `json:"updated_at"`
}

// TrafficLimitRuleRow mirrors the traffic_limit_rules table.
type TrafficLimitRuleRow struct {
	Scope             string `json:"scope"`
	Ref               string `json:"ref"`
	MonthlyLimitBytes int64  `json:"monthly_limit_bytes"`
	UpdatedAt         string `json:"updated_at"`
}

// ServerTagRow mirrors the server_tags table.
type ServerTagRow struct {
	ID        int64  `json:"id"`
	ServerID  int64  `json:"server_id"`
	Tag       string `json:"tag"`
	CreatedAt string `json:"created_at"`
}

// AlertChannelRow mirrors the alert_channels table. ChatID is not a secret (it
// is a public chat identifier); the Telegram bot token lives only in env.
type AlertChannelRow struct {
	ID              int64  `json:"id"`
	Kind            string `json:"kind"`
	Name            string `json:"name"`
	ChatID          string `json:"chat_id"`
	MessageTemplate string `json:"message_template"`
	Enabled         bool   `json:"enabled"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// AlertRuleRow mirrors the alert_rules table. ServerID/ChannelID are nullable.
type AlertRuleRow struct {
	ID              int64   `json:"id"`
	ServerID        *int64  `json:"server_id"`
	Metric          string  `json:"metric"`
	Comparator      string  `json:"comparator"`
	Threshold       float64 `json:"threshold"`
	ConsecutiveHits int     `json:"consecutive_hits"`
	CooldownSeconds int     `json:"cooldown_seconds"`
	Severity        string  `json:"severity"`
	ChannelID       *int64  `json:"channel_id"`
	Enabled         bool    `json:"enabled"`
	Note            string  `json:"note"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

// AlertSilenceRow mirrors the alert_silences table. ExpiresAt is nullable.
type AlertSilenceRow struct {
	ID        int64   `json:"id"`
	ServerID  int64   `json:"server_id"`
	Metric    string  `json:"metric"`
	Reason    string  `json:"reason"`
	ExpiresAt *string `json:"expires_at"`
	CreatedAt string  `json:"created_at"`
}

// InstallMetaRow mirrors the install_metadata table. InstalledAt is nullable.
type InstallMetaRow struct {
	ID          int64   `json:"id"`
	Domain      string  `json:"domain"`
	InstalledAt *string `json:"installed_at"`
	UpdatedAt   string  `json:"updated_at"`
}

// ExportOptions configures an export run.
type ExportOptions struct {
	// IncludeSecrets keeps credential_ref verbatim and embeds Env. When false
	// (the default), the export is secret-free.
	IncludeSecrets bool
	// Passphrase, when non-empty, encrypts the serialized document.
	Passphrase string
	AppVersion string
	Driver     string
	// Env is the set of sensitive environment values to embed; only used when
	// IncludeSecrets is true.
	Env map[string]string
}

// RestoreSummary reports what a successful import applied (or what a live
// database / archive currently holds).
type RestoreSummary struct {
	Servers             int
	ServerTags          int
	ServerTrafficLimits int
	TrafficLimitRules   int
	AlertChannels       int
	AlertRules          int
	AlertSilences       int
	InstallMetadata     int
	EnvKeys             int
}

// Summary returns the row and env counts carried by this archive.
func (a Archive) Summary() RestoreSummary {
	return RestoreSummary{
		Servers:             len(a.Data.Servers),
		ServerTags:          len(a.Data.ServerTags),
		ServerTrafficLimits: len(a.Data.ServerTrafficLimits),
		TrafficLimitRules:   len(a.Data.TrafficLimitRules),
		AlertChannels:       len(a.Data.AlertChannels),
		AlertRules:          len(a.Data.AlertRules),
		AlertSilences:       len(a.Data.AlertSilences),
		InstallMetadata:     len(a.Data.InstallMetadata),
		EnvKeys:             len(a.Env),
	}
}

// SummarizeDB returns row counts for the panel's backupable configuration tables.
func SummarizeDB(ctx context.Context, dbtx db.DBTX) (RestoreSummary, error) {
	if dbtx == nil {
		return RestoreSummary{}, errors.New("backup: nil database")
	}
	return summarizeDBCounts(ctx, dbtx)
}

// Export reads the configuration tables and returns the serialized backup
// bytes: indented JSON, optionally passphrase-encrypted. When
// opts.IncludeSecrets is false, servers' credential_ref is redacted and no
// environment values are written.
func Export(ctx context.Context, dbtx db.DBTX, opts ExportOptions) ([]byte, error) {
	if dbtx == nil {
		return nil, errors.New("backup: nil database")
	}

	archive := Archive{
		Format:          FormatPlain,
		Version:         SchemaVersion,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		AppVersion:      opts.AppVersion,
		SourceDriver:    opts.Driver,
		IncludesSecrets: opts.IncludeSecrets,
	}

	var err error
	if archive.Data.Servers, err = readServers(ctx, dbtx); err != nil {
		return nil, err
	}
	if archive.Data.ServerTags, err = readServerTags(ctx, dbtx); err != nil {
		return nil, err
	}
	if archive.Data.ServerTrafficLimits, err = readServerTrafficLimits(ctx, dbtx); err != nil {
		return nil, err
	}
	if archive.Data.TrafficLimitRules, err = readTrafficLimitRules(ctx, dbtx); err != nil {
		return nil, err
	}
	if archive.Data.AlertChannels, err = readAlertChannels(ctx, dbtx); err != nil {
		return nil, err
	}
	if archive.Data.AlertRules, err = readAlertRules(ctx, dbtx); err != nil {
		return nil, err
	}
	if archive.Data.AlertSilences, err = readAlertSilences(ctx, dbtx); err != nil {
		return nil, err
	}
	if archive.Data.InstallMetadata, err = readInstallMetadata(ctx, dbtx); err != nil {
		return nil, err
	}

	if opts.IncludeSecrets {
		if len(opts.Env) > 0 {
			archive.Env = opts.Env
		}
	} else {
		// Secret-free default: blank every credential reference so no plaintext
		// SSH password (stored/runtime strategy) or infrastructure reference
		// (external_ref) leaves the host.
		for i := range archive.Data.Servers {
			archive.Data.Servers[i].CredentialRef = ""
		}
	}

	return marshalArchive(archive, opts.Passphrase)
}

// PeekFormat reports the outer archive format marker ("nodexia-backup" or
// "nodexia-backup-encrypted") without decrypting the payload.
func PeekFormat(raw []byte) (string, error) {
	if len(raw) > MaxArchiveBytes {
		return "", ErrTooLarge
	}
	return peekFormat(raw)
}

// Inspect parses (and, when needed, decrypts) raw backup bytes into a validated
// Archive without touching the database. It enforces the format/version
// contract and referential integrity so callers can preview and reject before
// any destructive restore.
func Inspect(raw []byte, passphrase string) (Archive, error) {
	if len(raw) > MaxArchiveBytes {
		return Archive{}, ErrTooLarge
	}

	plaintext, err := decodeMaybeEncrypted(raw, passphrase)
	if err != nil {
		return Archive{}, err
	}

	archive, err := unmarshalArchive(plaintext)
	if err != nil {
		return Archive{}, err
	}

	if archive.Format != FormatPlain {
		return Archive{}, ErrBadFormat
	}
	if archive.Version != SchemaVersion {
		return Archive{}, fmt.Errorf("%w: found %d, supported %d", ErrUnsupportedVersion, archive.Version, SchemaVersion)
	}
	if err := validateReferences(archive.Data); err != nil {
		return Archive{}, err
	}
	return archive, nil
}

// validateReferences rejects an archive whose rows point at servers or channels
// that are not also present in the archive, contain duplicate primary keys or
// unique-constraint collisions, or violate column/enum invariants so a restore
// can never leave a dangling reference or fail mid-transaction.
func validateReferences(data Data) error {
	serverIDs := make(map[int64]struct{}, len(data.Servers))
	for _, s := range data.Servers {
		if s.ID <= 0 {
			return fmt.Errorf("%w: invalid server id %d", ErrMalformed, s.ID)
		}
		if _, exists := serverIDs[s.ID]; exists {
			return fmt.Errorf("%w: duplicate server id %d", ErrMalformed, s.ID)
		}
		if strings.TrimSpace(s.Name) == "" || strings.TrimSpace(s.Host) == "" || strings.TrimSpace(s.Username) == "" {
			return fmt.Errorf("%w: server %d missing required identity fields", ErrMalformed, s.ID)
		}
		if s.Port < 1 || s.Port > 65535 {
			return fmt.Errorf("%w: server %d invalid port %d", ErrMalformed, s.ID, s.Port)
		}
		if !isValidAuthMode(s.AuthMode) {
			return fmt.Errorf("%w: server %d invalid auth_mode %q", ErrMalformed, s.ID, s.AuthMode)
		}
		if !isValidCredentialStrategy(s.CredentialStrategy) {
			return fmt.Errorf("%w: server %d invalid credential_strategy %q", ErrMalformed, s.ID, s.CredentialStrategy)
		}
		if s.TrafficResetDay < 0 || s.TrafficResetDay > 28 {
			return fmt.Errorf("%w: server %d invalid traffic_reset_day %d", ErrMalformed, s.ID, s.TrafficResetDay)
		}
		serverIDs[s.ID] = struct{}{}
	}

	channelIDs := make(map[int64]struct{}, len(data.AlertChannels))
	for _, c := range data.AlertChannels {
		if c.ID <= 0 {
			return fmt.Errorf("%w: invalid alert channel id %d", ErrMalformed, c.ID)
		}
		if _, exists := channelIDs[c.ID]; exists {
			return fmt.Errorf("%w: duplicate alert channel id %d", ErrMalformed, c.ID)
		}
		if strings.ToLower(strings.TrimSpace(c.Kind)) != alerts.ChannelKindTelegram {
			return fmt.Errorf("%w: alert channel %d unsupported kind %q", ErrMalformed, c.ID, c.Kind)
		}
		if strings.TrimSpace(c.Name) == "" {
			return fmt.Errorf("%w: alert channel %d missing name", ErrMalformed, c.ID)
		}
		channelIDs[c.ID] = struct{}{}
	}

	tagIDs := make(map[int64]struct{}, len(data.ServerTags))
	tagKeys := make(map[string]struct{}, len(data.ServerTags))
	for _, t := range data.ServerTags {
		if _, ok := serverIDs[t.ServerID]; !ok {
			return fmt.Errorf("%w: tag references server %d", ErrDanglingReference, t.ServerID)
		}
		if t.ID <= 0 {
			return fmt.Errorf("%w: invalid server tag id %d", ErrMalformed, t.ID)
		}
		if _, exists := tagIDs[t.ID]; exists {
			return fmt.Errorf("%w: duplicate server tag id %d", ErrMalformed, t.ID)
		}
		tagIDs[t.ID] = struct{}{}

		normalized := strings.ToLower(strings.TrimSpace(t.Tag))
		if normalized == "" || len(normalized) > 40 {
			return fmt.Errorf("%w: invalid tag %q on server %d", ErrMalformed, t.Tag, t.ServerID)
		}
		key := fmt.Sprintf("%d:%s", t.ServerID, normalized)
		if _, exists := tagKeys[key]; exists {
			return fmt.Errorf("%w: duplicate tag %q on server %d", ErrMalformed, t.Tag, t.ServerID)
		}
		tagKeys[key] = struct{}{}
	}

	ruleIDs := make(map[int64]struct{}, len(data.AlertRules))
	for _, r := range data.AlertRules {
		if r.ServerID != nil {
			if _, ok := serverIDs[*r.ServerID]; !ok {
				return fmt.Errorf("%w: rule references server %d", ErrDanglingReference, *r.ServerID)
			}
		}
		if r.ChannelID != nil {
			if _, ok := channelIDs[*r.ChannelID]; !ok {
				return fmt.Errorf("%w: rule references channel %d", ErrDanglingReference, *r.ChannelID)
			}
		}
		if r.ID <= 0 {
			return fmt.Errorf("%w: invalid alert rule id %d", ErrMalformed, r.ID)
		}
		if _, exists := ruleIDs[r.ID]; exists {
			return fmt.Errorf("%w: duplicate alert rule id %d", ErrMalformed, r.ID)
		}
		ruleIDs[r.ID] = struct{}{}

		metric := strings.ToLower(strings.TrimSpace(r.Metric))
		if !alerts.IsRuleMetric(metric) {
			return fmt.Errorf("%w: alert rule %d invalid metric %q", ErrMalformed, r.ID, r.Metric)
		}
		if !isValidComparator(r.Comparator) {
			return fmt.Errorf("%w: alert rule %d invalid comparator %q", ErrMalformed, r.ID, r.Comparator)
		}
		if math.IsNaN(r.Threshold) || math.IsInf(r.Threshold, 0) {
			return fmt.Errorf("%w: alert rule %d invalid threshold", ErrMalformed, r.ID)
		}
		if r.ConsecutiveHits < 1 {
			return fmt.Errorf("%w: alert rule %d invalid consecutive_hits %d", ErrMalformed, r.ID, r.ConsecutiveHits)
		}
		if r.CooldownSeconds < 0 {
			return fmt.Errorf("%w: alert rule %d invalid cooldown_seconds %d", ErrMalformed, r.ID, r.CooldownSeconds)
		}
		if !alerts.IsSeverity(strings.ToLower(strings.TrimSpace(r.Severity))) {
			return fmt.Errorf("%w: alert rule %d invalid severity %q", ErrMalformed, r.ID, r.Severity)
		}
	}

	silenceIDs := make(map[int64]struct{}, len(data.AlertSilences))
	silenceKeys := make(map[string]struct{}, len(data.AlertSilences))
	for _, s := range data.AlertSilences {
		if _, ok := serverIDs[s.ServerID]; !ok {
			return fmt.Errorf("%w: silence references server %d", ErrDanglingReference, s.ServerID)
		}
		if s.ID <= 0 {
			return fmt.Errorf("%w: invalid alert silence id %d", ErrMalformed, s.ID)
		}
		if _, exists := silenceIDs[s.ID]; exists {
			return fmt.Errorf("%w: duplicate alert silence id %d", ErrMalformed, s.ID)
		}
		silenceIDs[s.ID] = struct{}{}

		metric := strings.ToLower(strings.TrimSpace(s.Metric))
		if !alerts.IsSilenceMetric(metric) {
			return fmt.Errorf("%w: alert silence %d invalid metric %q", ErrMalformed, s.ID, s.Metric)
		}
		key := fmt.Sprintf("%d:%s", s.ServerID, metric)
		if _, exists := silenceKeys[key]; exists {
			return fmt.Errorf("%w: duplicate alert silence for server %d metric %s", ErrMalformed, s.ServerID, s.Metric)
		}
		silenceKeys[key] = struct{}{}
	}

	trafficLimitServers := make(map[int64]struct{}, len(data.ServerTrafficLimits))
	for _, l := range data.ServerTrafficLimits {
		if _, ok := serverIDs[l.ServerID]; !ok {
			return fmt.Errorf("%w: traffic limit references server %d", ErrDanglingReference, l.ServerID)
		}
		if _, exists := trafficLimitServers[l.ServerID]; exists {
			return fmt.Errorf("%w: duplicate server traffic limit for server %d", ErrMalformed, l.ServerID)
		}
		if l.MonthlyLimitBytes < 0 {
			return fmt.Errorf("%w: invalid monthly_limit_bytes %d for server %d", ErrMalformed, l.MonthlyLimitBytes, l.ServerID)
		}
		if !isValidLimitKind(l.LimitKind) {
			return fmt.Errorf("%w: invalid limit_kind %q for server %d", ErrMalformed, l.LimitKind, l.ServerID)
		}
		trafficLimitServers[l.ServerID] = struct{}{}
	}

	ruleKeys := make(map[string]struct{}, len(data.TrafficLimitRules))
	for _, r := range data.TrafficLimitRules {
		scope := strings.ToLower(strings.TrimSpace(r.Scope))
		ref := strings.ToLower(strings.TrimSpace(r.Ref))
		if scope != analytics.LimitScopeGlobal && scope != analytics.LimitScopeTag {
			return fmt.Errorf("%w: invalid traffic limit rule scope %q", ErrMalformed, r.Scope)
		}
		if scope == analytics.LimitScopeGlobal && ref != "" {
			return fmt.Errorf("%w: global traffic limit rule must not specify ref %q", ErrMalformed, r.Ref)
		}
		if scope == analytics.LimitScopeTag && ref == "" {
			return fmt.Errorf("%w: tag traffic limit rule must specify non-empty ref", ErrMalformed)
		}
		if r.MonthlyLimitBytes < 0 {
			return fmt.Errorf("%w: invalid monthly_limit_bytes %d for traffic rule (%s, %s)", ErrMalformed, r.MonthlyLimitBytes, r.Scope, r.Ref)
		}
		key := scope + ":" + ref
		if _, exists := ruleKeys[key]; exists {
			return fmt.Errorf("%w: duplicate traffic limit rule (%s, %s)", ErrMalformed, r.Scope, r.Ref)
		}
		ruleKeys[key] = struct{}{}
	}

	metaIDs := make(map[int64]struct{}, len(data.InstallMetadata))
	for _, m := range data.InstallMetadata {
		if m.ID <= 0 {
			return fmt.Errorf("%w: invalid install_metadata id %d", ErrMalformed, m.ID)
		}
		if _, exists := metaIDs[m.ID]; exists {
			return fmt.Errorf("%w: duplicate install_metadata id %d", ErrMalformed, m.ID)
		}
		metaIDs[m.ID] = struct{}{}
	}
	return nil
}

func isValidAuthMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case servers.AuthModeKey, servers.AuthModePassword, servers.AuthModeHybrid:
		return true
	default:
		return false
	}
}

func isValidCredentialStrategy(strategy string) bool {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case servers.CredentialStrategyStored, servers.CredentialStrategyRuntime,
		servers.CredentialStrategyExternalRef, servers.CredentialStrategyAgentReady:
		return true
	default:
		return false
	}
}

func isValidComparator(comp string) bool {
	switch strings.ToLower(strings.TrimSpace(comp)) {
	case alerts.ComparatorGT, alerts.ComparatorGTE, alerts.ComparatorLT, alerts.ComparatorLTE:
		return true
	default:
		return false
	}
}

func isValidLimitKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", analytics.LimitKindRX, analytics.LimitKindTX, analytics.LimitKindTotal:
		return true
	default:
		return false
	}
}

// Import applies a validated Archive with replace semantics inside a single
// transaction: the configuration tables are truncated, then reloaded preserving
// the original primary keys. Any error rolls the whole thing back so a failed
// import leaves the panel exactly as it was. Env values are reported in the
// summary but never applied — they belong in the host environment.
func Import(ctx context.Context, conn *sql.DB, archive Archive) (RestoreSummary, error) {
	if conn == nil {
		return RestoreSummary{}, errors.New("backup: nil database")
	}
	// Re-validate defensively: a caller may construct an Archive without going
	// through Inspect.
	if archive.Format != FormatPlain {
		return RestoreSummary{}, ErrBadFormat
	}
	if archive.Version != SchemaVersion {
		return RestoreSummary{}, ErrUnsupportedVersion
	}
	if err := validateReferences(archive.Data); err != nil {
		return RestoreSummary{}, err
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return RestoreSummary{}, fmt.Errorf("backup: begin restore transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback() // no-op after a successful Commit
	}()

	// Delete children before parents. Removing servers cascades to their
	// telemetry and alert rows (ON DELETE CASCADE), which is acceptable for a
	// replace restore and documented to the operator.
	for _, stmt := range []string{
		`DELETE FROM alert_silences`,
		`DELETE FROM alert_rules`,
		`DELETE FROM alert_channels`,
		`DELETE FROM traffic_limit_rules`,
		`DELETE FROM server_traffic_limits`,
		`DELETE FROM server_tags`,
		`DELETE FROM servers`,
		`DELETE FROM install_metadata`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return RestoreSummary{}, fmt.Errorf("backup: clear table: %w", err)
		}
	}

	if err := insertServers(ctx, tx, archive.Data.Servers); err != nil {
		return RestoreSummary{}, err
	}
	if err := insertServerTags(ctx, tx, archive.Data.ServerTags); err != nil {
		return RestoreSummary{}, err
	}
	if err := insertServerTrafficLimits(ctx, tx, archive.Data.ServerTrafficLimits); err != nil {
		return RestoreSummary{}, err
	}
	if err := insertTrafficLimitRules(ctx, tx, archive.Data.TrafficLimitRules); err != nil {
		return RestoreSummary{}, err
	}
	if err := insertAlertChannels(ctx, tx, archive.Data.AlertChannels); err != nil {
		return RestoreSummary{}, err
	}
	if err := insertAlertRules(ctx, tx, archive.Data.AlertRules); err != nil {
		return RestoreSummary{}, err
	}
	if err := insertAlertSilences(ctx, tx, archive.Data.AlertSilences); err != nil {
		return RestoreSummary{}, err
	}
	if err := insertInstallMetadata(ctx, tx, archive.Data.InstallMetadata); err != nil {
		return RestoreSummary{}, err
	}

	isMySQL := isMySQLDriver(conn)
	if !isMySQL {
		if err := syncSQLiteSequences(ctx, tx); err != nil {
			return RestoreSummary{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return RestoreSummary{}, fmt.Errorf("backup: commit restore transaction: %w", err)
	}

	if isMySQL {
		if err := syncMySQLAutoIncrement(ctx, conn); err != nil {
			return RestoreSummary{}, err
		}
	}

	return archive.Summary(), nil
}
