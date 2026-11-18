package webhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	assets "github.com/Ho3einK84/Nodexia"
	"github.com/Ho3einK84/Nodexia/internal/backup"
	"github.com/Ho3einK84/Nodexia/internal/commandstream"
	webhttp "github.com/Ho3einK84/Nodexia/internal/http"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/logging"
	"github.com/Ho3einK84/Nodexia/internal/module/registry"
	"github.com/Ho3einK84/Nodexia/internal/scheduler"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

var csrfPattern = regexp.MustCompile(`name="_csrf_token" value="([^"]+)"`)

// TestBackupExportImportOverHTTP drives the export and restore endpoints through
// the full middleware chain (auth, session, CSRF), exercising the subtle
// multipart-upload + CSRF interaction that a unit test would miss.
func TestBackupExportImportOverHTTP(t *testing.T) {
	cfg := testutil.TestConfig(t)
	logging.Setup(cfg.Log)

	runtime := testutil.OpenTestDB(t)
	// Seed one server so the export has content to round-trip.
	if _, err := runtime.SQL.ExecContext(context.Background(),
		`INSERT INTO servers (id, name, host, port, auth_mode, username, note, credential_strategy, credential_ref, created_at, updated_at)
		 VALUES (1, 'edge', '10.0.0.9', 22, 'password', 'root', '', 'stored', 'topsecret', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	renderer, err := view.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	staticFiles, err := assets.Static()
	if err != nil {
		t.Fatalf("Static: %v", err)
	}
	handler := webhttp.NewRouter(cfg, runtime, sshclient.New(cfg.SSH, cfg.Security),
		commandstream.New(0), nil, nil, renderer, staticFiles, nil, registry.DefaultModules())
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	authToken, err := middleware.GenerateAuthToken(cfg.Security.AdminUsername, []byte(cfg.Security.SessionSecret), cfg.Security.SessionTTL)
	if err != nil {
		t.Fatalf("GenerateAuthToken: %v", err)
	}
	authCookie := middleware.BuildAuthCookie(authToken, cfg.Security.SessionTTL, cfg.Security.SessionCookieSecure)

	// GET the page to obtain a session cookie and the CSRF token.
	getReq, _ := http.NewRequest(http.MethodGet, server.URL+"/ops/diagnostics", nil)
	getReq.AddCookie(authCookie)
	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("GET diagnostics: %v", err)
	}
	defer closeResponseBody(getResp)
	body := readBody(t, getResp)

	var sessionCookie *http.Cookie
	for _, c := range getResp.Cookies() {
		if c.Name == cfg.Security.SessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie issued")
	}
	match := csrfPattern.FindStringSubmatch(body)
	if match == nil {
		t.Fatal("could not find CSRF token in page")
	}
	csrf := match[1]

	// Export (urlencoded form) with the secrets opt-in.
	form := url.Values{"_csrf_token": {csrf}, "include_secrets": {"on"}}
	exportReq, _ := http.NewRequest(http.MethodPost, server.URL+"/ops/backup/export", strings.NewReader(form.Encode()))
	exportReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	exportReq.Header.Set("Origin", server.URL)
	exportReq.AddCookie(authCookie)
	exportReq.AddCookie(sessionCookie)
	exportResp, err := http.DefaultClient.Do(exportReq)
	if err != nil {
		t.Fatalf("POST export: %v", err)
	}
	defer closeResponseBody(exportResp)
	if exportResp.StatusCode != http.StatusOK {
		t.Fatalf("export status = %d", exportResp.StatusCode)
	}
	if cd := exportResp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Fatalf("export missing attachment disposition: %q", cd)
	}
	backupBytes := []byte(readBody(t, exportResp))
	if !bytes.Contains(backupBytes, []byte("topsecret")) {
		t.Fatal("secrets opt-in export should contain the credential")
	}

	// Wipe the server so we can prove the restore brings it back.
	if _, err := runtime.SQL.ExecContext(context.Background(), `DELETE FROM servers`); err != nil {
		t.Fatalf("wipe: %v", err)
	}

	// Import (multipart) — CSRF token rides in the query string.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("confirm", "on"); err != nil {
		t.Fatalf("write multipart confirmation: %v", err)
	}
	fw, err := mw.CreateFormFile("backup_file", "backup.json")
	if err != nil {
		t.Fatalf("create multipart backup file: %v", err)
	}
	if _, err := fw.Write(backupBytes); err != nil {
		t.Fatalf("write multipart backup file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("finalize multipart body: %v", err)
	}

	importURL := server.URL + "/ops/backup/import"
	importReq, _ := http.NewRequest(http.MethodPost, importURL, &buf)
	importReq.Header.Set("Content-Type", mw.FormDataContentType())
	importReq.Header.Set("Origin", server.URL)
	importReq.Header.Set("X-CSRF-Token", csrf)
	importReq.AddCookie(authCookie)
	importReq.AddCookie(sessionCookie)
	importResp, err := http.DefaultClient.Do(importReq)
	if err != nil {
		t.Fatalf("POST import: %v", err)
	}
	defer closeResponseBody(importResp)
	if importResp.StatusCode != http.StatusOK {
		t.Fatalf("import status = %d, body = %q", importResp.StatusCode, readBody(t, importResp))
	}
	importBody := readBody(t, importResp)
	if !strings.Contains(importBody, "Backup restored") {
		t.Fatalf("import did not report success: %q", importBody)
	}

	var count int
	if err := runtime.SQL.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM servers WHERE id = 1`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("restore did not recreate the server (count=%d)", count)
	}
}

// TestBackupExportRejectsMissingCSRF confirms the export endpoint is protected
// by the shared CSRF middleware.
func TestBackupExportRejectsMissingCSRF(t *testing.T) {
	cfg := testutil.TestConfig(t)
	logging.Setup(cfg.Log)
	runtime := testutil.OpenTestDB(t)
	renderer, err := view.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	staticFiles, _ := assets.Static()
	handler := webhttp.NewRouter(cfg, runtime, sshclient.New(cfg.SSH, cfg.Security),
		commandstream.New(0), nil, nil, renderer, staticFiles, nil, registry.DefaultModules())
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	authToken, _ := middleware.GenerateAuthToken(cfg.Security.AdminUsername, []byte(cfg.Security.SessionSecret), cfg.Security.SessionTTL)
	authCookie := middleware.BuildAuthCookie(authToken, cfg.Security.SessionTTL, cfg.Security.SessionCookieSecure)

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/ops/backup/export", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", server.URL)
	req.AddCookie(authCookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer closeResponseBody(resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF token, got %d", resp.StatusCode)
	}
}

func TestBackupInspectEndpointAndNoQueryCSRF(t *testing.T) {
	cfg := testutil.TestConfig(t)
	logging.Setup(cfg.Log)
	runtime := testutil.OpenTestDB(t)
	if _, err := runtime.SQL.ExecContext(context.Background(),
		`INSERT INTO servers (id, name, host, port, auth_mode, username, note, credential_strategy, credential_ref, created_at, updated_at)
		 VALUES (1, 'edge-1', '10.0.0.9', 22, 'password', 'root', '', 'stored', 'topsecret', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	if _, err := runtime.SQL.ExecContext(context.Background(),
		`INSERT INTO server_tags (id, server_id, tag, created_at) VALUES (1, 1, 'eu', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("seed tag: %v", err)
	}

	renderer, err := view.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	staticFiles, _ := assets.Static()
	sshSvc := sshclient.New(cfg.SSH, cfg.Security)
	sched := scheduler.New(cfg, runtime.SQL, sshSvc)
	handler := webhttp.NewRouter(cfg, runtime, sshSvc,
		commandstream.New(0), nil, nil, renderer, staticFiles, sched, registry.DefaultModules())
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	authToken, _ := middleware.GenerateAuthToken(cfg.Security.AdminUsername, []byte(cfg.Security.SessionSecret), cfg.Security.SessionTTL)
	authCookie := middleware.BuildAuthCookie(authToken, cfg.Security.SessionTTL, cfg.Security.SessionCookieSecure)

	// 1. Diagnostics page must NOT embed ?_csrf_token= in the import form action.
	getReq, _ := http.NewRequest(http.MethodGet, server.URL+"/ops/diagnostics", nil)
	getReq.AddCookie(authCookie)
	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("GET diagnostics: %v", err)
	}
	defer closeResponseBody(getResp)
	diagBody := readBody(t, getResp)
	if strings.Contains(diagBody, "/ops/backup/import?_csrf_token") {
		t.Fatal("diagnostics template leaked _csrf_token into import action query string")
	}

	var sessionCookie *http.Cookie
	for _, c := range getResp.Cookies() {
		if c.Name == cfg.Security.SessionCookieName {
			sessionCookie = c
		}
	}
	match := csrfPattern.FindStringSubmatch(diagBody)
	if match == nil {
		t.Fatal("missing CSRF token on diagnostics page")
	}
	csrf := match[1]

	// 2. Encrypted export using X-CSRF-Token header.
	encForm := url.Values{"include_secrets": {"on"}, "passphrase": {"pass-123"}}
	exportReq, _ := http.NewRequest(http.MethodPost, server.URL+"/ops/backup/export", strings.NewReader(encForm.Encode()))
	exportReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	exportReq.Header.Set("X-CSRF-Token", csrf)
	exportReq.Header.Set("Origin", server.URL)
	exportReq.AddCookie(authCookie)
	exportReq.AddCookie(sessionCookie)
	exportResp, err := http.DefaultClient.Do(exportReq)
	if err != nil {
		t.Fatalf("POST export: %v", err)
	}
	defer closeResponseBody(exportResp)
	if exportResp.StatusCode != http.StatusOK {
		t.Fatalf("encrypted export status = %d", exportResp.StatusCode)
	}
	encBytes := []byte(readBody(t, exportResp))

	postInspect := func(payload []byte, passphrase string, useHeader bool) *http.Response {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("passphrase", passphrase)
		if !useHeader {
			_ = mw.WriteField("_csrf_token", csrf)
		}
		fw, _ := mw.CreateFormFile("backup_file", "backup.json.enc")
		_, _ = fw.Write(payload)
		_ = mw.Close()

		req, _ := http.NewRequest(http.MethodPost, server.URL+"/ops/backup/inspect", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("Origin", server.URL)
		if useHeader {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		req.AddCookie(authCookie)
		req.AddCookie(sessionCookie)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST inspect: %v", err)
		}
		return resp
	}

	// 3. Inspect without passphrase -> passphrase_required.
	r1 := postInspect(encBytes, "", true)
	defer closeResponseBody(r1)
	if r1.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 when passphrase omitted, got %d", r1.StatusCode)
	}
	var j1 map[string]any
	if err := json.Unmarshal([]byte(readBody(t, r1)), &j1); err != nil {
		t.Fatalf("decode j1: %v", err)
	}
	if j1["code"] != "passphrase_required" || j1["encrypted"] != true {
		t.Fatalf("unexpected inspect missing-passphrase JSON: %+v", j1)
	}

	// 4. Inspect with wrong passphrase -> wrong_passphrase.
	r2 := postInspect(encBytes, "wrong", true)
	defer closeResponseBody(r2)
	if r2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 when passphrase wrong, got %d", r2.StatusCode)
	}
	var j2 map[string]any
	_ = json.Unmarshal([]byte(readBody(t, r2)), &j2)
	if j2["code"] != "wrong_passphrase" || j2["encrypted"] != true {
		t.Fatalf("unexpected inspect wrong-passphrase JSON: %+v", j2)
	}

	// 5. Inspect with correct passphrase + multipart CSRF (no header, no query string).
	r3 := postInspect(encBytes, "pass-123", false)
	defer closeResponseBody(r3)
	if r3.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on valid inspect, got %d: %s", r3.StatusCode, readBody(t, r3))
	}
	var j3 struct {
		OK              bool           `json:"ok"`
		Encrypted       bool           `json:"encrypted"`
		IncludesSecrets bool           `json:"includes_secrets"`
		Counts          map[string]int `json:"counts"`
	}
	if err := json.Unmarshal([]byte(readBody(t, r3)), &j3); err != nil {
		t.Fatalf("decode j3: %v", err)
	}
	if !j3.OK || !j3.Encrypted || !j3.IncludesSecrets || j3.Counts["servers"] != 1 || j3.Counts["server_tags"] != 1 {
		t.Fatalf("unexpected valid inspect JSON: %+v", j3)
	}

	// 6. Oversized upload -> 413 Request Entity Too Large.
	oversize := make([]byte, backup.MaxArchiveBytes+16)
	r4 := postInspect(oversize, "", true)
	defer closeResponseBody(r4)
	if r4.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 on oversized upload, got %d", r4.StatusCode)
	}

	// 7. Wipe DB and scheduler, then restore WITHOUT query string CSRF -> scheduler immediately sees server 1 again.
	if _, err := runtime.SQL.ExecContext(context.Background(), `DELETE FROM servers`); err != nil {
		t.Fatalf("wipe: %v", err)
	}
	sched.Refresh(context.Background())
	if ov := sched.Overview(50); len(ov.Jobs) != 0 {
		t.Fatalf("expected 0 scheduler jobs after wipe, got %d", len(ov.Jobs))
	}

	var impBuf bytes.Buffer
	impMW := multipart.NewWriter(&impBuf)
	_ = impMW.WriteField("_csrf_token", csrf)
	_ = impMW.WriteField("confirm", "on")
	_ = impMW.WriteField("passphrase", "pass-123")
	fw, _ := impMW.CreateFormFile("backup_file", "backup.json.enc")
	_, _ = fw.Write(encBytes)
	_ = impMW.Close()

	impReq, _ := http.NewRequest(http.MethodPost, server.URL+"/ops/backup/import", &impBuf)
	impReq.Header.Set("Content-Type", impMW.FormDataContentType())
	impReq.Header.Set("X-CSRF-Token", csrf)
	impReq.Header.Set("Origin", server.URL)
	impReq.AddCookie(authCookie)
	impReq.AddCookie(sessionCookie)
	impResp, err := http.DefaultClient.Do(impReq)
	if err != nil {
		t.Fatalf("POST import: %v", err)
	}
	defer closeResponseBody(impResp)
	if impResp.StatusCode != http.StatusOK {
		t.Fatalf("import status = %d, body = %s", impResp.StatusCode, readBody(t, impResp))
	}
	if ov := sched.Overview(50); len(ov.Jobs) == 0 {
		t.Fatal("scheduler should immediately reload jobs after backup restore")
	}
}
