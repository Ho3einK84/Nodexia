package webhttp_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	assets "github.com/Ho3einK84/Nodexia"
	"github.com/Ho3einK84/Nodexia/internal/commandstream"
	webhttp "github.com/Ho3einK84/Nodexia/internal/http"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/logging"
	"github.com/Ho3einK84/Nodexia/internal/module/registry"
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
	body := readBody(t, getResp)
	getResp.Body.Close()

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
	if exportResp.StatusCode != http.StatusOK {
		t.Fatalf("export status = %d", exportResp.StatusCode)
	}
	if cd := exportResp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Fatalf("export missing attachment disposition: %q", cd)
	}
	backupBytes := []byte(readBody(t, exportResp))
	exportResp.Body.Close()
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
	_ = mw.WriteField("confirm", "on")
	fw, _ := mw.CreateFormFile("backup_file", "backup.json")
	_, _ = fw.Write(backupBytes)
	mw.Close()

	importURL := server.URL + "/ops/backup/import?_csrf_token=" + url.QueryEscape(csrf)
	importReq, _ := http.NewRequest(http.MethodPost, importURL, &buf)
	importReq.Header.Set("Content-Type", mw.FormDataContentType())
	importReq.Header.Set("Origin", server.URL)
	importReq.AddCookie(authCookie)
	importReq.AddCookie(sessionCookie)
	importResp, err := http.DefaultClient.Do(importReq)
	if err != nil {
		t.Fatalf("POST import: %v", err)
	}
	if importResp.StatusCode != http.StatusOK {
		t.Fatalf("import status = %d, body = %q", importResp.StatusCode, readBody(t, importResp))
	}
	importBody := readBody(t, importResp)
	importResp.Body.Close()
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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF token, got %d", resp.StatusCode)
	}
}
