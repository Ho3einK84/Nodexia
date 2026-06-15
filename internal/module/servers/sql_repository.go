package servers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrNotFound = errors.New("servers: not found")

type SQLRepository struct {
	conn *sql.DB
}

func NewSQLRepository(conn *sql.DB) SQLRepository {
	return SQLRepository{conn: conn}
}

func (r SQLRepository) Create(ctx context.Context, server Server) (Server, error) {
	server = normalizeServer(server)
	now := time.Now().UTC()

	tx, err := r.conn.BeginTx(ctx, nil)
	if err != nil {
		return Server{}, fmt.Errorf("servers: begin create transaction: %w", err)
	}

	result, err := tx.ExecContext(
		ctx,
		`INSERT INTO servers (name, host, port, auth_mode, username, note, credential_strategy, credential_ref, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		server.Name,
		server.Host,
		server.Port,
		server.AuthMode,
		server.Username,
		server.Note,
		server.CredentialStrategy,
		server.CredentialRef,
		now,
		now,
	)
	if err != nil {
		_ = tx.Rollback()
		return Server{}, fmt.Errorf("servers: create: %w", err)
	}

	insertedID, err := result.LastInsertId()
	if err != nil {
		_ = tx.Rollback()
		return Server{}, fmt.Errorf("servers: read last insert id: %w", err)
	}

	if err := replaceTags(ctx, tx, insertedID, server.Tags); err != nil {
		_ = tx.Rollback()
		return Server{}, err
	}

	if err := tx.Commit(); err != nil {
		return Server{}, fmt.Errorf("servers: commit create transaction: %w", err)
	}

	return r.GetByID(ctx, insertedID)
}

func (r SQLRepository) GetByID(ctx context.Context, id int64) (Server, error) {
	row := r.conn.QueryRowContext(
		ctx,
		`SELECT id, name, host, port, auth_mode, username, note, credential_strategy, credential_ref, country_code, country_name, country_checked_at, created_at, updated_at
		 FROM servers
		 WHERE id = ?
		 LIMIT 1`,
		id,
	)

	server, err := scanServer(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Server{}, ErrNotFound
		}

		return Server{}, fmt.Errorf("servers: get by id %d: %w", id, err)
	}

	tags, err := loadTags(ctx, r.conn, server.ID)
	if err != nil {
		return Server{}, fmt.Errorf("servers: load tags for %d: %w", server.ID, err)
	}

	server.Tags = tags
	return server, nil
}

func (r SQLRepository) List(ctx context.Context) ([]Server, error) {
	rows, err := r.conn.QueryContext(
		ctx,
		`SELECT id, name, host, port, auth_mode, username, note, credential_strategy, credential_ref, country_code, country_name, country_checked_at, created_at, updated_at
		 FROM servers
		 ORDER BY id DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("servers: list: %w", err)
	}
	defer rows.Close()

	var serversList []Server
	var serverIDs []int64
	for rows.Next() {
		server, err := scanServer(rows)
		if err != nil {
			return nil, fmt.Errorf("servers: scan row: %w", err)
		}
		serversList = append(serversList, server)
		serverIDs = append(serverIDs, server.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("servers: iterate rows: %w", err)
	}

	tagMap, err := loadTagsBatch(ctx, r.conn, serverIDs)
	if err != nil {
		return nil, fmt.Errorf("servers: load tags batch: %w", err)
	}
	for i := range serversList {
		serversList[i].Tags = tagMap[serversList[i].ID]
	}

	return serversList, nil
}

func (r SQLRepository) Update(ctx context.Context, server Server) (Server, error) {
	server = normalizeServer(server)
	server.UpdatedAt = time.Now().UTC()

	tx, err := r.conn.BeginTx(ctx, nil)
	if err != nil {
		return Server{}, fmt.Errorf("servers: begin update transaction: %w", err)
	}

	result, err := tx.ExecContext(
		ctx,
		`UPDATE servers
		 SET name = ?, host = ?, port = ?, auth_mode = ?, username = ?, note = ?, credential_strategy = ?, credential_ref = ?, updated_at = ?
		 WHERE id = ?`,
		server.Name,
		server.Host,
		server.Port,
		server.AuthMode,
		server.Username,
		server.Note,
		server.CredentialStrategy,
		server.CredentialRef,
		server.UpdatedAt,
		server.ID,
	)
	if err != nil {
		_ = tx.Rollback()
		return Server{}, fmt.Errorf("servers: update %d: %w", server.ID, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return Server{}, fmt.Errorf("servers: update rows affected %d: %w", server.ID, err)
	}

	if rowsAffected == 0 {
		_ = tx.Rollback()
		return Server{}, ErrNotFound
	}

	if err := replaceTags(ctx, tx, server.ID, server.Tags); err != nil {
		_ = tx.Rollback()
		return Server{}, err
	}

	if err := tx.Commit(); err != nil {
		return Server{}, fmt.Errorf("servers: commit update transaction: %w", err)
	}

	return r.GetByID(ctx, server.ID)
}

// UpdateCountry writes the detected country (or an empty result) for one server
// and stamps country_checked_at. It deliberately updates only the country
// columns — not updated_at or any credential/identity field — so detection never
// interferes with the audit trail of user edits. A missing server is a no-op
// (returns nil): the row may have been deleted between detection and write, and
// that is not an error worth surfacing to a background job.
func (r SQLRepository) UpdateCountry(ctx context.Context, id int64, code, name string) error {
	code = strings.TrimSpace(code)
	name = strings.TrimSpace(name)
	now := time.Now().UTC()

	if _, err := r.conn.ExecContext(
		ctx,
		`UPDATE servers
		 SET country_code = ?, country_name = ?, country_checked_at = ?
		 WHERE id = ?`,
		code,
		name,
		now,
		id,
	); err != nil {
		return fmt.Errorf("servers: update country %d: %w", id, err)
	}

	return nil
}

func (r SQLRepository) Delete(ctx context.Context, id int64) error {
	tx, err := r.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("servers: begin delete transaction: %w", err)
	}

	for _, statement := range []string{
		`DELETE FROM server_tags WHERE server_id = ?`,
		`DELETE FROM command_history WHERE server_id = ?`,
		`DELETE FROM node_snapshots WHERE server_id = ?`,
		`DELETE FROM system_snapshots WHERE server_id = ?`,
		`DELETE FROM server_system_facts WHERE server_id = ?`,
		`DELETE FROM vnstat_snapshots WHERE server_id = ?`,
		`DELETE FROM alert_events WHERE server_id = ?`,
		`DELETE FROM alert_silences WHERE server_id = ?`,
		`DELETE FROM alert_rules WHERE server_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, id); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("servers: delete related records for %d: %w", id, err)
		}
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM servers WHERE id = ?`, id)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("servers: delete %d: %w", id, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("servers: delete rows affected %d: %w", id, err)
	}

	if rowsAffected == 0 {
		_ = tx.Rollback()
		return ErrNotFound
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("servers: commit delete transaction: %w", err)
	}

	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanServer(scanner rowScanner) (Server, error) {
	var server Server
	var createdAtRaw any
	var updatedAtRaw any
	var countryCheckedAtRaw any

	if err := scanner.Scan(
		&server.ID,
		&server.Name,
		&server.Host,
		&server.Port,
		&server.AuthMode,
		&server.Username,
		&server.Note,
		&server.CredentialStrategy,
		&server.CredentialRef,
		&server.CountryCode,
		&server.CountryName,
		&countryCheckedAtRaw,
		&createdAtRaw,
		&updatedAtRaw,
	); err != nil {
		return Server{}, err
	}

	createdAt, err := parseDatabaseTime(createdAtRaw)
	if err != nil {
		return Server{}, fmt.Errorf("parse created_at: %w", err)
	}

	updatedAt, err := parseDatabaseTime(updatedAtRaw)
	if err != nil {
		return Server{}, fmt.Errorf("parse updated_at: %w", err)
	}

	// country_checked_at is nullable; a NULL maps to a zero time (never checked).
	countryCheckedAt, err := parseDatabaseTime(countryCheckedAtRaw)
	if err != nil {
		return Server{}, fmt.Errorf("parse country_checked_at: %w", err)
	}

	server.CountryCheckedAt = countryCheckedAt
	server.CreatedAt = createdAt
	server.UpdatedAt = updatedAt
	return server, nil
}

func normalizeServer(server Server) Server {
	server.Name = strings.TrimSpace(server.Name)
	server.Host = strings.TrimSpace(server.Host)
	server.AuthMode = strings.TrimSpace(server.AuthMode)
	server.Username = strings.TrimSpace(server.Username)
	server.Note = strings.TrimSpace(server.Note)
	server.CredentialStrategy = strings.TrimSpace(server.CredentialStrategy)
	server.CredentialRef = strings.TrimSpace(server.CredentialRef)

	if server.Port == 0 {
		server.Port = 22
	}

	if server.CredentialStrategy == "" {
		server.CredentialStrategy = CredentialStrategyStored
	}

	return server
}

func parseDatabaseTime(value any) (time.Time, error) {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC(), nil
	case string:
		return parseTimeString(typed)
	case []byte:
		return parseTimeString(string(typed))
	case nil:
		return time.Time{}, nil
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

func loadTags(ctx context.Context, queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}, serverID int64) ([]string, error) {
	rows, err := queryer.QueryContext(
		ctx,
		`SELECT tag
		 FROM server_tags
		 WHERE server_id = ?
		 ORDER BY tag ASC`,
		serverID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}

	return tags, rows.Err()
}

func loadTagsBatch(ctx context.Context, db *sql.DB, serverIDs []int64) (map[int64][]string, error) {
	if len(serverIDs) == 0 {
		return map[int64][]string{}, nil
	}

	placeholders := strings.Repeat("?,", len(serverIDs)-1) + "?"
	args := make([]any, len(serverIDs))
	for i, id := range serverIDs {
		args[i] = id
	}

	rows, err := db.QueryContext(
		ctx,
		`SELECT server_id, tag FROM server_tags WHERE server_id IN (`+placeholders+`) ORDER BY tag ASC`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64][]string, len(serverIDs))
	for rows.Next() {
		var serverID int64
		var tag string
		if err := rows.Scan(&serverID, &tag); err != nil {
			return nil, err
		}
		result[serverID] = append(result[serverID], tag)
	}
	return result, rows.Err()
}

func replaceTags(ctx context.Context, execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}, serverID int64, tags []string) error {
	if _, err := execer.ExecContext(ctx, `DELETE FROM server_tags WHERE server_id = ?`, serverID); err != nil {
		return fmt.Errorf("servers: clear tags for %d: %w", serverID, err)
	}

	for _, tag := range tags {
		if _, err := execer.ExecContext(
			ctx,
			`INSERT INTO server_tags (server_id, tag, created_at) VALUES (?, ?, ?)`,
			serverID,
			tag,
			time.Now().UTC(),
		); err != nil {
			return fmt.Errorf("servers: insert tag %q for %d: %w", tag, serverID, err)
		}
	}

	return nil
}
