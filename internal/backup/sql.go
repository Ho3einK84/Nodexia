package backup

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/db"
)

// readServers loads every server row in a stable order. credential_ref is read
// verbatim here; redaction (when secrets are not requested) happens in Export.
func readServers(ctx context.Context, dbtx db.DBTX) ([]ServerRow, error) {
	rows, err := dbtx.QueryContext(ctx,
		`SELECT id, name, host, port, auth_mode, username, note, credential_strategy, credential_ref, country_code, country_name, created_at, updated_at
		 FROM servers ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("backup: query servers: %w", err)
	}
	defer rows.Close()

	var out []ServerRow
	for rows.Next() {
		var r ServerRow
		var createdRaw, updatedRaw any
		if err := rows.Scan(&r.ID, &r.Name, &r.Host, &r.Port, &r.AuthMode, &r.Username, &r.Note,
			&r.CredentialStrategy, &r.CredentialRef, &r.CountryCode, &r.CountryName, &createdRaw, &updatedRaw); err != nil {
			return nil, fmt.Errorf("backup: scan server: %w", err)
		}
		if r.CreatedAt, err = formatDBTime(createdRaw); err != nil {
			return nil, fmt.Errorf("backup: server created_at: %w", err)
		}
		if r.UpdatedAt, err = formatDBTime(updatedRaw); err != nil {
			return nil, fmt.Errorf("backup: server updated_at: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func readServerTags(ctx context.Context, dbtx db.DBTX) ([]ServerTagRow, error) {
	rows, err := dbtx.QueryContext(ctx,
		`SELECT id, server_id, tag, created_at FROM server_tags ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("backup: query server_tags: %w", err)
	}
	defer rows.Close()

	var out []ServerTagRow
	for rows.Next() {
		var r ServerTagRow
		var createdRaw any
		if err := rows.Scan(&r.ID, &r.ServerID, &r.Tag, &createdRaw); err != nil {
			return nil, fmt.Errorf("backup: scan server_tag: %w", err)
		}
		if r.CreatedAt, err = formatDBTime(createdRaw); err != nil {
			return nil, fmt.Errorf("backup: server_tag created_at: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func readAlertChannels(ctx context.Context, dbtx db.DBTX) ([]AlertChannelRow, error) {
	rows, err := dbtx.QueryContext(ctx,
		`SELECT id, kind, name, chat_id, message_template, enabled, created_at, updated_at
		 FROM alert_channels ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("backup: query alert_channels: %w", err)
	}
	defer rows.Close()

	var out []AlertChannelRow
	for rows.Next() {
		var r AlertChannelRow
		var enabled int64
		var createdRaw, updatedRaw any
		if err := rows.Scan(&r.ID, &r.Kind, &r.Name, &r.ChatID, &r.MessageTemplate, &enabled, &createdRaw, &updatedRaw); err != nil {
			return nil, fmt.Errorf("backup: scan alert_channel: %w", err)
		}
		r.Enabled = enabled != 0
		if r.CreatedAt, err = formatDBTime(createdRaw); err != nil {
			return nil, fmt.Errorf("backup: alert_channel created_at: %w", err)
		}
		if r.UpdatedAt, err = formatDBTime(updatedRaw); err != nil {
			return nil, fmt.Errorf("backup: alert_channel updated_at: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func readAlertRules(ctx context.Context, dbtx db.DBTX) ([]AlertRuleRow, error) {
	rows, err := dbtx.QueryContext(ctx,
		`SELECT id, server_id, metric, comparator, threshold, consecutive_hits, cooldown_seconds, severity, channel_id, enabled, note, created_at, updated_at
		 FROM alert_rules ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("backup: query alert_rules: %w", err)
	}
	defer rows.Close()

	var out []AlertRuleRow
	for rows.Next() {
		var r AlertRuleRow
		var serverID, channelID sql.NullInt64
		var enabled int64
		var createdRaw, updatedRaw any
		if err := rows.Scan(&r.ID, &serverID, &r.Metric, &r.Comparator, &r.Threshold, &r.ConsecutiveHits,
			&r.CooldownSeconds, &r.Severity, &channelID, &enabled, &r.Note, &createdRaw, &updatedRaw); err != nil {
			return nil, fmt.Errorf("backup: scan alert_rule: %w", err)
		}
		r.ServerID = nullableToPtr(serverID)
		r.ChannelID = nullableToPtr(channelID)
		r.Enabled = enabled != 0
		if r.CreatedAt, err = formatDBTime(createdRaw); err != nil {
			return nil, fmt.Errorf("backup: alert_rule created_at: %w", err)
		}
		if r.UpdatedAt, err = formatDBTime(updatedRaw); err != nil {
			return nil, fmt.Errorf("backup: alert_rule updated_at: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func readAlertSilences(ctx context.Context, dbtx db.DBTX) ([]AlertSilenceRow, error) {
	rows, err := dbtx.QueryContext(ctx,
		`SELECT id, server_id, metric, reason, expires_at, created_at FROM alert_silences ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("backup: query alert_silences: %w", err)
	}
	defer rows.Close()

	var out []AlertSilenceRow
	for rows.Next() {
		var r AlertSilenceRow
		var expiresRaw, createdRaw any
		if err := rows.Scan(&r.ID, &r.ServerID, &r.Metric, &r.Reason, &expiresRaw, &createdRaw); err != nil {
			return nil, fmt.Errorf("backup: scan alert_silence: %w", err)
		}
		if r.ExpiresAt, err = formatNullableDBTime(expiresRaw); err != nil {
			return nil, fmt.Errorf("backup: alert_silence expires_at: %w", err)
		}
		if r.CreatedAt, err = formatDBTime(createdRaw); err != nil {
			return nil, fmt.Errorf("backup: alert_silence created_at: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func readInstallMetadata(ctx context.Context, dbtx db.DBTX) ([]InstallMetaRow, error) {
	rows, err := dbtx.QueryContext(ctx,
		`SELECT id, domain, installed_at, updated_at FROM install_metadata ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("backup: query install_metadata: %w", err)
	}
	defer rows.Close()

	var out []InstallMetaRow
	for rows.Next() {
		var r InstallMetaRow
		var installedRaw, updatedRaw any
		if err := rows.Scan(&r.ID, &r.Domain, &installedRaw, &updatedRaw); err != nil {
			return nil, fmt.Errorf("backup: scan install_metadata: %w", err)
		}
		if r.InstalledAt, err = formatNullableDBTime(installedRaw); err != nil {
			return nil, fmt.Errorf("backup: install_metadata installed_at: %w", err)
		}
		if r.UpdatedAt, err = formatDBTime(updatedRaw); err != nil {
			return nil, fmt.Errorf("backup: install_metadata updated_at: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func insertServers(ctx context.Context, tx *sql.Tx, rows []ServerRow) error {
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO servers (id, name, host, port, auth_mode, username, note, credential_strategy, credential_ref, country_code, country_name, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.ID, r.Name, r.Host, r.Port, r.AuthMode, r.Username, r.Note, r.CredentialStrategy, r.CredentialRef,
			r.CountryCode, r.CountryName, restoreTime(r.CreatedAt), restoreTime(r.UpdatedAt)); err != nil {
			return fmt.Errorf("backup: insert server %d: %w", r.ID, err)
		}
	}
	return nil
}

func insertServerTags(ctx context.Context, tx *sql.Tx, rows []ServerTagRow) error {
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO server_tags (id, server_id, tag, created_at) VALUES (?, ?, ?, ?)`,
			r.ID, r.ServerID, r.Tag, restoreTime(r.CreatedAt)); err != nil {
			return fmt.Errorf("backup: insert server_tag %d: %w", r.ID, err)
		}
	}
	return nil
}

func insertAlertChannels(ctx context.Context, tx *sql.Tx, rows []AlertChannelRow) error {
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO alert_channels (id, kind, name, chat_id, message_template, enabled, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			r.ID, r.Kind, r.Name, r.ChatID, r.MessageTemplate, boolToInt(r.Enabled),
			restoreTime(r.CreatedAt), restoreTime(r.UpdatedAt)); err != nil {
			return fmt.Errorf("backup: insert alert_channel %d: %w", r.ID, err)
		}
	}
	return nil
}

func insertAlertRules(ctx context.Context, tx *sql.Tx, rows []AlertRuleRow) error {
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO alert_rules (id, server_id, metric, comparator, threshold, consecutive_hits, cooldown_seconds, severity, channel_id, enabled, note, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.ID, ptrToNullable(r.ServerID), r.Metric, r.Comparator, r.Threshold, r.ConsecutiveHits,
			r.CooldownSeconds, r.Severity, ptrToNullable(r.ChannelID), boolToInt(r.Enabled), r.Note,
			restoreTime(r.CreatedAt), restoreTime(r.UpdatedAt)); err != nil {
			return fmt.Errorf("backup: insert alert_rule %d: %w", r.ID, err)
		}
	}
	return nil
}

func insertAlertSilences(ctx context.Context, tx *sql.Tx, rows []AlertSilenceRow) error {
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO alert_silences (id, server_id, metric, reason, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			r.ID, r.ServerID, r.Metric, r.Reason, restoreNullableTime(r.ExpiresAt), restoreTime(r.CreatedAt)); err != nil {
			return fmt.Errorf("backup: insert alert_silence %d: %w", r.ID, err)
		}
	}
	return nil
}

func insertInstallMetadata(ctx context.Context, tx *sql.Tx, rows []InstallMetaRow) error {
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO install_metadata (id, domain, installed_at, updated_at) VALUES (?, ?, ?, ?)`,
			r.ID, r.Domain, restoreNullableTime(r.InstalledAt), restoreTime(r.UpdatedAt)); err != nil {
			return fmt.Errorf("backup: insert install_metadata %d: %w", r.ID, err)
		}
	}
	return nil
}

// nullableToPtr converts a SQL NULL-aware int into a *int64 for JSON.
func nullableToPtr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	out := v.Int64
	return &out
}

// ptrToNullable returns a bind value (nil → SQL NULL) for a nullable int.
func ptrToNullable(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// formatDBTime normalizes a scanned datetime value to an RFC3339 string. A NULL
// or zero value becomes the empty string.
func formatDBTime(v any) (string, error) {
	t, err := parseDBTime(v)
	if err != nil {
		return "", err
	}
	if t.IsZero() {
		return "", nil
	}
	return t.UTC().Format(time.RFC3339Nano), nil
}

// formatNullableDBTime is formatDBTime for columns that are genuinely nullable:
// a NULL/zero value becomes a nil pointer (absent in JSON).
func formatNullableDBTime(v any) (*string, error) {
	s, err := formatDBTime(v)
	if err != nil {
		return nil, err
	}
	if s == "" {
		return nil, nil
	}
	return &s, nil
}

// restoreTime parses an RFC3339 string back to a time.Time for binding. An empty
// or unparseable value falls back to now so a required NOT NULL column is never
// left blank.
func restoreTime(s string) time.Time {
	if t, ok := parseRFC3339(s); ok {
		return t
	}
	return time.Now().UTC()
}

// restoreNullableTime binds a nullable RFC3339 string (nil/empty → SQL NULL).
func restoreNullableTime(s *string) any {
	if s == nil {
		return nil
	}
	if t, ok := parseRFC3339(*s); ok {
		return t
	}
	return nil
}

func parseRFC3339(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}

// parseDBTime accepts the assorted shapes a datetime column can take across the
// SQLite and MySQL drivers (time.Time, string, []byte, nil).
func parseDBTime(value any) (time.Time, error) {
	switch typed := value.(type) {
	case nil:
		return time.Time{}, nil
	case time.Time:
		return typed.UTC(), nil
	case string:
		return parseTimeString(typed)
	case []byte:
		return parseTimeString(string(typed))
	default:
		return time.Time{}, fmt.Errorf("unsupported time type %T", value)
	}
}

func parseTimeString(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time value %q", value)
}
