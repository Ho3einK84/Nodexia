package backup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/testutil"
)

const secretPassword = "super-secret-ssh-pass"

// seed populates one of every config table, including a server whose
// credential_ref holds a plaintext SSH password and an alert rule referencing
// both that server and a channel.
func seed(t *testing.T, conn *sql.DB) {
	t.Helper()
	ctx := context.Background()
	stmts := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO servers (id, name, host, port, auth_mode, username, note, credential_strategy, credential_ref, created_at, updated_at)
		  VALUES (1, 'edge-1', '10.0.0.1', 22, 'password', 'root', 'primary', 'stored', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, []any{secretPassword}},
		{`INSERT INTO server_tags (id, server_id, tag, created_at) VALUES (1, 1, 'prod', CURRENT_TIMESTAMP)`, nil},
		{`INSERT INTO alert_channels (id, kind, name, chat_id, message_template, enabled, created_at, updated_at)
		  VALUES (1, 'telegram', 'ops', '12345', '', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, nil},
		{`INSERT INTO alert_rules (id, server_id, metric, comparator, threshold, consecutive_hits, cooldown_seconds, severity, channel_id, enabled, note, created_at, updated_at)
		  VALUES (1, 1, 'cpu', 'gte', 90, 3, 900, 'warning', 1, 1, 'hot box', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, nil},
		{`INSERT INTO alert_rules (id, server_id, metric, comparator, threshold, consecutive_hits, cooldown_seconds, severity, channel_id, enabled, note, created_at, updated_at)
		  VALUES (2, NULL, 'disk', 'gte', 95, 1, 900, 'critical', NULL, 1, 'global rule', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, nil},
		{`INSERT INTO alert_silences (id, server_id, metric, reason, expires_at, created_at) VALUES (1, 1, 'cpu', 'maintenance', NULL, CURRENT_TIMESTAMP)`, nil},
		{`INSERT INTO install_metadata (id, domain, installed_at, updated_at) VALUES (1, 'panel.example', NULL, CURRENT_TIMESTAMP)`, nil},
	}
	for _, s := range stmts {
		if _, err := conn.ExecContext(ctx, s.q, s.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := testutil.OpenTestDB(t)
	seed(t, src.SQL)

	raw, err := Export(ctx, src.SQL, ExportOptions{IncludeSecrets: true, AppVersion: "test", Driver: "sqlite"})
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	archive, err := Inspect(raw, "")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(archive.Data.Servers) != 1 || archive.Data.Servers[0].CredentialRef != secretPassword {
		t.Fatalf("expected secret credential preserved with opt-in, got %+v", archive.Data.Servers)
	}
	if len(archive.Data.AlertRules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(archive.Data.AlertRules))
	}

	// Restore into a fresh database and re-export; the data must match exactly.
	dst := testutil.OpenTestDB(t)
	summary, err := Import(ctx, dst.SQL, archive)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if summary.Servers != 1 || summary.AlertRules != 2 || summary.AlertSilences != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	raw2, err := Export(ctx, dst.SQL, ExportOptions{IncludeSecrets: true})
	if err != nil {
		t.Fatalf("re-export: %v", err)
	}
	archive2, err := Inspect(raw2, "")
	if err != nil {
		t.Fatalf("re-inspect: %v", err)
	}
	if !reflect.DeepEqual(archive.Data, archive2.Data) {
		t.Fatalf("round-trip mismatch:\n before: %+v\n after:  %+v", archive.Data, archive2.Data)
	}
}

func TestExportRedactsSecretsByDefault(t *testing.T) {
	ctx := context.Background()
	src := testutil.OpenTestDB(t)
	seed(t, src.SQL)

	raw, err := Export(ctx, src.SQL, ExportOptions{
		IncludeSecrets: false,
		Env:            map[string]string{"NODEXIA_AUTH_PASSWORD": "should-not-appear"},
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if bytes.Contains(raw, []byte(secretPassword)) {
		t.Fatalf("default export leaked the stored SSH credential")
	}
	if bytes.Contains(raw, []byte("should-not-appear")) {
		t.Fatalf("default export leaked an environment secret")
	}

	archive, err := Inspect(raw, "")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	for _, s := range archive.Data.Servers {
		if s.CredentialRef != "" {
			t.Fatalf("server %d credential_ref not redacted: %q", s.ID, s.CredentialRef)
		}
	}
	if archive.IncludesSecrets {
		t.Fatalf("includes_secrets should be false in a default export")
	}
	if len(archive.Env) != 0 {
		t.Fatalf("env must be empty in a default export, got %v", archive.Env)
	}
}

func TestEncryptedRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := testutil.OpenTestDB(t)
	seed(t, src.SQL)

	const pass = "correct horse battery staple"
	raw, err := Export(ctx, src.SQL, ExportOptions{IncludeSecrets: true, Passphrase: pass})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if bytes.Contains(raw, []byte(secretPassword)) {
		t.Fatalf("encrypted export leaked plaintext secret")
	}

	if _, err := Inspect(raw, ""); !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("expected ErrPassphraseRequired, got %v", err)
	}
	if _, err := Inspect(raw, "wrong"); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("expected ErrWrongPassphrase, got %v", err)
	}

	archive, err := Inspect(raw, pass)
	if err != nil {
		t.Fatalf("inspect with passphrase: %v", err)
	}
	if archive.Data.Servers[0].CredentialRef != secretPassword {
		t.Fatalf("decrypted archive missing secret")
	}
}

func TestInspectRejectsBadFiles(t *testing.T) {
	if _, err := Inspect([]byte("not json at all"), ""); !errors.Is(err, ErrMalformed) {
		t.Fatalf("garbage: expected ErrMalformed, got %v", err)
	}
	if _, err := Inspect([]byte(`{"hello":"world"}`), ""); !errors.Is(err, ErrBadFormat) {
		t.Fatalf("no format: expected ErrBadFormat, got %v", err)
	}

	future := mustJSON(t, Archive{Format: FormatPlain, Version: SchemaVersion + 1})
	if _, err := Inspect(future, ""); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("future version: expected ErrUnsupportedVersion, got %v", err)
	}

	serverID := int64(999)
	dangling := mustJSON(t, Archive{
		Format:  FormatPlain,
		Version: SchemaVersion,
		Data: Data{
			AlertRules: []AlertRuleRow{{ID: 1, ServerID: &serverID, Metric: "cpu"}},
		},
	})
	if _, err := Inspect(dangling, ""); !errors.Is(err, ErrDanglingReference) {
		t.Fatalf("dangling ref: expected ErrDanglingReference, got %v", err)
	}
}

func TestImportIsAtomicOnBadReference(t *testing.T) {
	ctx := context.Background()
	dst := testutil.OpenTestDB(t)
	seed(t, dst.SQL)

	serverID := int64(404)
	bad := Archive{
		Format:  FormatPlain,
		Version: SchemaVersion,
		Data:    Data{AlertSilences: []AlertSilenceRow{{ID: 1, ServerID: serverID, Metric: "cpu"}}},
	}
	if _, err := Import(ctx, dst.SQL, bad); !errors.Is(err, ErrDanglingReference) {
		t.Fatalf("expected ErrDanglingReference, got %v", err)
	}

	// The original seeded data must be untouched after the rejected import.
	var count int
	if err := dst.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM servers`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected seed data intact, found %d servers", count)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
