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
		{`INSERT INTO servers (id, name, host, port, auth_mode, username, note, credential_strategy, credential_ref, traffic_reset_day, created_at, updated_at)
		  VALUES (1, 'edge-1', '10.0.0.1', 22, 'password', 'root', 'primary', 'stored', ?, 15, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, []any{secretPassword}},
		{`INSERT INTO server_tags (id, server_id, tag, created_at) VALUES (1, 1, 'prod', CURRENT_TIMESTAMP)`, nil},
		{`INSERT INTO server_traffic_limits (server_id, monthly_limit_bytes, limit_kind, updated_at) VALUES (1, 1073741824000, 'rx', CURRENT_TIMESTAMP)`, nil},
		{`INSERT INTO traffic_limit_rules (scope, ref, monthly_limit_bytes, updated_at) VALUES ('global', '', 2147483648000, CURRENT_TIMESTAMP)`, nil},
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
	if archive.Data.Servers[0].TrafficResetDay != 15 {
		t.Fatalf("expected traffic_reset_day=15, got %d", archive.Data.Servers[0].TrafficResetDay)
	}
	if len(archive.Data.AlertRules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(archive.Data.AlertRules))
	}
	if len(archive.Data.ServerTrafficLimits) != 1 || len(archive.Data.TrafficLimitRules) != 1 {
		t.Fatalf("expected traffic limits preserved, got %+v", archive.Data)
	}

	// Restore into a fresh database and re-export; the data must match exactly.
	dst := testutil.OpenTestDB(t)
	summary, err := Import(ctx, dst.SQL, archive)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if summary.Servers != 1 || summary.AlertRules != 2 || summary.AlertSilences != 1 || summary.ServerTrafficLimits != 1 || summary.TrafficLimitRules != 1 {
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

func TestInspectRejectsDuplicateKeys(t *testing.T) {
	// Duplicate server ID
	dupServer := mustJSON(t, Archive{
		Format:  FormatPlain,
		Version: SchemaVersion,
		Data: Data{
			Servers: []ServerRow{
				{ID: 1, Name: "s1"},
				{ID: 1, Name: "s2"},
			},
		},
	})
	if _, err := Inspect(dupServer, ""); !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed on duplicate server id, got %v", err)
	}

	// Duplicate traffic limit rule
	dupRule := mustJSON(t, Archive{
		Format:  FormatPlain,
		Version: SchemaVersion,
		Data: Data{
			TrafficLimitRules: []TrafficLimitRuleRow{
				{Scope: "global", Ref: "", MonthlyLimitBytes: 100},
				{Scope: "global", Ref: "", MonthlyLimitBytes: 200},
			},
		},
	})
	if _, err := Inspect(dupRule, ""); !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed on duplicate traffic limit rule, got %v", err)
	}

	// Invalid scope
	badScope := mustJSON(t, Archive{
		Format:  FormatPlain,
		Version: SchemaVersion,
		Data: Data{
			TrafficLimitRules: []TrafficLimitRuleRow{
				{Scope: "invalid", Ref: ""},
			},
		},
	})
	if _, err := Inspect(badScope, ""); !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed on invalid scope, got %v", err)
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

func validArchiveFixture() Archive {
	serverID := int64(1)
	channelID := int64(1)
	return Archive{
		Format:  FormatPlain,
		Version: SchemaVersion,
		Data: Data{
			Servers: []ServerRow{
				{
					ID:                 1,
					Name:               "edge-1",
					Host:               "10.0.0.1",
					Port:               22,
					AuthMode:           "password",
					Username:           "root",
					CredentialStrategy: "stored",
					TrafficResetDay:    1,
				},
			},
			ServerTags: []ServerTagRow{
				{ID: 1, ServerID: 1, Tag: "prod"},
			},
			ServerTrafficLimits: []ServerTrafficLimitRow{
				{ServerID: 1, MonthlyLimitBytes: 1000, LimitKind: "rx"},
			},
			TrafficLimitRules: []TrafficLimitRuleRow{
				{Scope: "global", Ref: "", MonthlyLimitBytes: 2000},
			},
			AlertChannels: []AlertChannelRow{
				{ID: 1, Kind: "telegram", Name: "ops", ChatID: "123"},
			},
			AlertRules: []AlertRuleRow{
				{
					ID:              1,
					ServerID:        &serverID,
					Metric:          "cpu",
					Comparator:      "gte",
					Threshold:       90,
					ConsecutiveHits: 2,
					CooldownSeconds: 300,
					Severity:        "warning",
					ChannelID:       &channelID,
					Enabled:         true,
				},
			},
			AlertSilences: []AlertSilenceRow{
				{ID: 1, ServerID: 1, Metric: "cpu"},
			},
			InstallMetadata: []InstallMetaRow{
				{ID: 1, Domain: "panel.example"},
			},
		},
	}
}

func TestInspectRejectsCorruptAndInvalidFields(t *testing.T) {
	t.Run("empty and non-object JSON", func(t *testing.T) {
		for _, payload := range [][]byte{{}, []byte("   "), []byte("null"), []byte("[]")} {
			if _, err := Inspect(payload, ""); !errors.Is(err, ErrMalformed) {
				t.Fatalf("payload %q: expected ErrMalformed, got %v", payload, err)
			}
		}
	})

	t.Run("trailing bytes after valid archive", func(t *testing.T) {
		valid := mustJSON(t, validArchiveFixture())
		withTrailing := append(valid, []byte(`{"extra":true}`)...)
		if _, err := Inspect(withTrailing, ""); !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected ErrMalformed for trailing bytes, got %v", err)
		}
	})

	t.Run("oversized archive", func(t *testing.T) {
		huge := make([]byte, MaxArchiveBytes+1)
		if _, err := Inspect(huge, ""); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("expected ErrTooLarge, got %v", err)
		}
	})

	cases := []struct {
		name   string
		mutate func(*Archive)
	}{
		{"zero server id", func(a *Archive) { a.Data.Servers[0].ID = 0 }},
		{"negative channel id", func(a *Archive) { a.Data.AlertChannels[0].ID = -2 }},
		{"invalid port 0", func(a *Archive) { a.Data.Servers[0].Port = 0 }},
		{"invalid port 70000", func(a *Archive) { a.Data.Servers[0].Port = 70000 }},
		{"invalid auth_mode", func(a *Archive) { a.Data.Servers[0].AuthMode = "telnet" }},
		{"invalid credential_strategy", func(a *Archive) { a.Data.Servers[0].CredentialStrategy = "magic" }},
		{"invalid traffic_reset_day", func(a *Archive) { a.Data.Servers[0].TrafficResetDay = 29 }},
		{"duplicate tag per server case-insensitive", func(a *Archive) {
			a.Data.ServerTags = append(a.Data.ServerTags, ServerTagRow{ID: 2, ServerID: 1, Tag: " PROD "})
		}},
		{"invalid limit_kind", func(a *Archive) { a.Data.ServerTrafficLimits[0].LimitKind = "both" }},
		{"negative limit bytes", func(a *Archive) { a.Data.ServerTrafficLimits[0].MonthlyLimitBytes = -1 }},
		{"global limit rule with ref", func(a *Archive) { a.Data.TrafficLimitRules[0].Ref = "edge" }},
		{"tag limit rule empty ref", func(a *Archive) {
			a.Data.TrafficLimitRules[0] = TrafficLimitRuleRow{Scope: "tag", Ref: "", MonthlyLimitBytes: 100}
		}},
		{"invalid channel kind", func(a *Archive) { a.Data.AlertChannels[0].Kind = "slack" }},
		{"invalid rule comparator", func(a *Archive) { a.Data.AlertRules[0].Comparator = "eq" }},
		{"invalid rule severity", func(a *Archive) { a.Data.AlertRules[0].Severity = "fatal" }},
		{"invalid rule metric", func(a *Archive) { a.Data.AlertRules[0].Metric = "gpu" }},
		{"zero consecutive hits", func(a *Archive) { a.Data.AlertRules[0].ConsecutiveHits = 0 }},
		{"negative cooldown", func(a *Archive) { a.Data.AlertRules[0].CooldownSeconds = -1 }},
		{"invalid silence metric", func(a *Archive) { a.Data.AlertSilences[0].Metric = "gpu" }},
		{"duplicate install metadata id", func(a *Archive) {
			a.Data.InstallMetadata = append(a.Data.InstallMetadata, InstallMetaRow{ID: 1, Domain: "other"})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := validArchiveFixture()
			tc.mutate(&a)
			if _, err := Inspect(mustJSON(t, a), ""); !errors.Is(err, ErrMalformed) {
				t.Fatalf("%s: expected ErrMalformed, got %v", tc.name, err)
			}
		})
	}
}

func TestImportSyncsSequencesAndAllowsSubsequentInserts(t *testing.T) {
	ctx := context.Background()
	dst := testutil.OpenTestDB(t)

	// Force-create sqlite_sequence on the test database via a dummy AUTOINCREMENT
	// table to exercise both branches (sqlite_sequence update AND normal MAX+1).
	if _, err := dst.SQL.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS _seq_probe (id INTEGER PRIMARY KEY AUTOINCREMENT, v TEXT)`); err != nil {
		t.Fatalf("create _seq_probe: %v", err)
	}
	if _, err := dst.SQL.ExecContext(ctx, `INSERT INTO _seq_probe (v) VALUES ('init')`); err != nil {
		t.Fatalf("seed _seq_probe: %v", err)
	}

	a := validArchiveFixture()
	serverID := int64(42)
	channelID := int64(17)
	a.Data.Servers[0].ID = serverID
	a.Data.ServerTags[0] = ServerTagRow{ID: 88, ServerID: serverID, Tag: "edge"}
	a.Data.ServerTrafficLimits[0].ServerID = serverID
	a.Data.AlertChannels[0].ID = channelID
	a.Data.AlertRules[0].ID = 99
	a.Data.AlertRules[0].ServerID = &serverID
	a.Data.AlertRules[0].ChannelID = &channelID
	a.Data.AlertSilences[0] = AlertSilenceRow{ID: 55, ServerID: serverID, Metric: "cpu"}
	a.Data.InstallMetadata[0].ID = 7

	summary, err := Import(ctx, dst.SQL, a)
	if err != nil {
		t.Fatalf("import high PKs: %v", err)
	}
	if summary.Servers != 1 || summary.InstallMetadata != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	// Verify sqlite_sequence tracked the restored high PKs.
	var seq int64
	if err := dst.SQL.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name = 'servers'`).Scan(&seq); err != nil {
		t.Fatalf("query sqlite_sequence: %v", err)
	}
	if seq != 42 {
		t.Fatalf("expected sqlite_sequence servers=42, got %d", seq)
	}

	// Subsequent natural inserts without explicit IDs must not collide.
	res, err := dst.SQL.ExecContext(ctx,
		`INSERT INTO servers (name, host, port, auth_mode, username, note, credential_strategy, credential_ref)
		 VALUES ('edge-next', '10.0.0.2', 22, 'password', 'root', '', 'stored', '')`)
	if err != nil {
		t.Fatalf("post-restore server insert failed: %v", err)
	}
	newServerID, err := res.LastInsertId()
	if err != nil || newServerID <= 42 {
		t.Fatalf("expected new server id > 42, got %d (err=%v)", newServerID, err)
	}

	if _, err := dst.SQL.ExecContext(ctx, `INSERT INTO alert_channels (kind, name, chat_id) VALUES ('telegram', 'ops-2', '999')`); err != nil {
		t.Fatalf("post-restore channel insert failed: %v", err)
	}
	if _, err := dst.SQL.ExecContext(ctx, `INSERT INTO server_tags (server_id, tag) VALUES (?, 'next')`, newServerID); err != nil {
		t.Fatalf("post-restore tag insert failed: %v", err)
	}
	if _, err := dst.SQL.ExecContext(ctx, `INSERT INTO alert_rules (server_id, metric, comparator, threshold, consecutive_hits, cooldown_seconds, severity) VALUES (?, 'cpu', 'gte', 80, 1, 60, 'info')`, newServerID); err != nil {
		t.Fatalf("post-restore rule insert failed: %v", err)
	}
	if _, err := dst.SQL.ExecContext(ctx, `INSERT INTO alert_silences (server_id, metric) VALUES (?, 'ram')`, newServerID); err != nil {
		t.Fatalf("post-restore silence insert failed: %v", err)
	}

	counts, err := SummarizeDB(ctx, dst.SQL)
	if err != nil {
		t.Fatalf("SummarizeDB: %v", err)
	}
	if counts.Servers != 2 || counts.ServerTags != 2 || counts.AlertChannels != 2 || counts.AlertRules != 2 || counts.AlertSilences != 2 {
		t.Fatalf("unexpected SummarizeDB counts after post-restore inserts: %+v", counts)
	}
}
