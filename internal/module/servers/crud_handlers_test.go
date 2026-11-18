package servers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

func testServerDeps(t *testing.T) (module.Dependencies, servers.Repository) {
	t.Helper()
	cfg := testutil.TestConfig(t)
	runtime := testutil.OpenTestDB(t)
	renderer, err := view.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	repo := servers.NewSQLRepository(runtime.SQL)
	return module.Dependencies{
		Config:   cfg,
		Database: runtime,
		Renderer: renderer,
	}, repo
}

func TestEditHandlerNeverExposesStoredSecretPassword(t *testing.T) {
	deps, repo := testServerDeps(t)
	const secret = "SuperSecretSSHPassword!987"
	created, err := repo.Create(context.Background(), servers.Server{
		Name:               "edge-1",
		Host:               "198.51.100.10",
		Port:               22,
		AuthMode:           servers.AuthModePassword,
		Username:           "root",
		CredentialStrategy: servers.CredentialStrategyStored,
		CredentialRef:      secret,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	h := servers.NewEditHandler(deps, repo)
	req := httptest.NewRequest(http.MethodGet, "/servers/"+strconv.FormatInt(created.ID, 10)+"/edit", nil)
	req.SetPathValue("id", strconv.FormatInt(created.ID, 10))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("EditHandler status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, secret) {
		t.Fatal("rendered server edit HTML leaked plaintext stored SSH password")
	}
	if !strings.Contains(body, "Leave blank to keep existing password") {
		t.Error("expected keep-existing password placeholder when stored credential exists")
	}
}

func TestEditHandlerKeepsNonSecretExternalReference(t *testing.T) {
	deps, repo := testServerDeps(t)
	created, err := repo.Create(context.Background(), servers.Server{
		Name:               "ext-1",
		Host:               "198.51.100.11",
		Port:               22,
		AuthMode:           servers.AuthModePassword,
		Username:           "root",
		CredentialStrategy: servers.CredentialStrategyExternalRef,
		CredentialRef:      "password_env=NODEXIA_EDGE_PASS",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	h := servers.NewEditHandler(deps, repo)
	req := httptest.NewRequest(http.MethodGet, "/servers/"+strconv.FormatInt(created.ID, 10)+"/edit", nil)
	req.SetPathValue("id", strconv.FormatInt(created.ID, 10))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("EditHandler status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "password_env=NODEXIA_EDGE_PASS") {
		t.Error("expected non-secret external_ref pointer to stay editable")
	}
}

func TestUpdateHandlerPreservesBlankStoredPasswordAndAvoidsLeakOnValidationError(t *testing.T) {
	deps, repo := testServerDeps(t)
	const initialSecret = "InitialSecret#42"
	created, err := repo.Create(context.Background(), servers.Server{
		Name:               "node-a",
		Host:               "198.51.100.20",
		Port:               22,
		AuthMode:           servers.AuthModePassword,
		Username:           "ubuntu",
		CredentialStrategy: servers.CredentialStrategyStored,
		CredentialRef:      initialSecret,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	h := servers.NewUpdateHandler(deps, repo)
	sid := strconv.FormatInt(created.ID, 10)

	// 1. Validation failure (empty name) with blank password must NOT leak initialSecret
	//    and MUST keep the "keep existing password" hint.
	badForm := url.Values{
		"name":                {""},
		"host":                {"198.51.100.20"},
		"port":                {"22"},
		"auth_mode":           {servers.AuthModePassword},
		"username":            {"ubuntu"},
		"credential_strategy": {servers.CredentialStrategyStored},
		"credential_ref":      {""},
	}
	badReq := httptest.NewRequest(http.MethodPost, "/servers/"+sid+"/edit", strings.NewReader(badForm.Encode()))
	badReq.SetPathValue("id", sid)
	badReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badRec := httptest.NewRecorder()
	h.ServeHTTP(badRec, badReq)

	if badRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid update status = %d, want 422", badRec.Code)
	}
	if strings.Contains(badRec.Body.String(), initialSecret) {
		t.Fatal("validation re-render leaked plaintext SSH password")
	}
	if !strings.Contains(badRec.Body.String(), "Leave blank to keep existing password") {
		t.Error("validation re-render dropped keep-existing password placeholder")
	}

	// 2. Valid update with blank password must preserve initialSecret in DB.
	okForm := url.Values{
		"name":                {"node-a-renamed"},
		"host":                {"198.51.100.20"},
		"port":                {"2222"},
		"auth_mode":           {servers.AuthModePassword},
		"username":            {"ubuntu"},
		"credential_strategy": {servers.CredentialStrategyStored},
		"credential_ref":      {""},
	}
	okReq := httptest.NewRequest(http.MethodPost, "/servers/"+sid+"/edit", strings.NewReader(okForm.Encode()))
	okReq.SetPathValue("id", sid)
	okReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	okRec := httptest.NewRecorder()
	h.ServeHTTP(okRec, okReq)

	if okRec.Code != http.StatusSeeOther {
		t.Fatalf("valid update status = %d, want 303", okRec.Code)
	}
	updated, err := repo.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if updated.Name != "node-a-renamed" || updated.Port != 2222 || updated.CredentialRef != initialSecret {
		t.Fatalf("updated = %+v, want preserved CredentialRef %q", updated, initialSecret)
	}

	// 3. Explicit new password replaces stored credential.
	rotateForm := url.Values{
		"name":                {"node-a-renamed"},
		"host":                {"198.51.100.20"},
		"port":                {"2222"},
		"auth_mode":           {servers.AuthModePassword},
		"username":            {"ubuntu"},
		"credential_strategy": {servers.CredentialStrategyStored},
		"credential_ref":      {"RotatedSecret!99"},
	}
	rotReq := httptest.NewRequest(http.MethodPost, "/servers/"+sid+"/edit", strings.NewReader(rotateForm.Encode()))
	rotReq.SetPathValue("id", sid)
	rotReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rotRec := httptest.NewRecorder()
	h.ServeHTTP(rotRec, rotReq)

	if rotRec.Code != http.StatusSeeOther {
		t.Fatalf("rotate update status = %d, want 303", rotRec.Code)
	}
	rotated, err := repo.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID() after rotate error = %v", err)
	}
	if rotated.CredentialRef != "RotatedSecret!99" {
		t.Fatalf("rotated CredentialRef = %q, want RotatedSecret!99", rotated.CredentialRef)
	}
}
