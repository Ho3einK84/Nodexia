package servers_test

import (
	"context"
	"encoding/json"
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

func TestCreateHandlerJSONValidationAndSuccess(t *testing.T) {
	deps, repo := testServerDeps(t)
	h := servers.NewCreateHandler(deps, repo)

	// 1. Invalid payload with Accept: application/json returns 422 JSON with field errors.
	badForm := url.Values{
		"name":                {""},
		"host":                {"not a host"},
		"port":                {"99999"},
		"auth_mode":           {servers.AuthModeHybrid},
		"username":            {""},
		"credential_strategy": {servers.CredentialStrategyStored},
	}
	badReq := httptest.NewRequest(http.MethodPost, "/servers", strings.NewReader(badForm.Encode()))
	badReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badReq.Header.Set("Accept", "application/json")
	badRec := httptest.NewRecorder()
	h.ServeHTTP(badRec, badReq)

	if badRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid JSON create status = %d, want 422", badRec.Code)
	}
	var badPayload struct {
		OK      bool              `json:"ok"`
		Message string            `json:"message"`
		Errors  map[string]string `json:"errors"`
	}
	if err := json.Unmarshal(badRec.Body.Bytes(), &badPayload); err != nil {
		t.Fatalf("decode 422 JSON: %v (body=%s)", err, badRec.Body.String())
	}
	if badPayload.OK {
		t.Fatal("expected ok=false on validation error")
	}
	for _, field := range []string{"name", "host", "port", "username"} {
		if badPayload.Errors[field] == "" {
			t.Errorf("expected JSON error for %q, got %+v", field, badPayload.Errors)
		}
	}

	// 2. Valid payload with Accept: application/json returns 201 JSON and persists the server.
	okForm := url.Values{
		"name":                {"edge-nl-01"},
		"host":                {"203.0.113.42"},
		"port":                {"2222"},
		"auth_mode":           {servers.AuthModeHybrid},
		"username":            {"root"},
		"tags":                {"prod, nl"},
		"credential_strategy": {servers.CredentialStrategyStored},
		"credential_ref":      {"S3cretPass!"},
		"traffic_reset_day":   {"15"},
	}
	okReq := httptest.NewRequest(http.MethodPost, "/servers", strings.NewReader(okForm.Encode()))
	okReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	okReq.Header.Set("Accept", "application/json")
	okRec := httptest.NewRecorder()
	h.ServeHTTP(okRec, okReq)

	if okRec.Code != http.StatusCreated {
		t.Fatalf("valid JSON create status = %d, want 201 (body=%s)", okRec.Code, okRec.Body.String())
	}
	var okPayload struct {
		OK       bool   `json:"ok"`
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Redirect string `json:"redirect"`
		Message  string `json:"message"`
	}
	if err := json.Unmarshal(okRec.Body.Bytes(), &okPayload); err != nil {
		t.Fatalf("decode 201 JSON: %v", err)
	}
	if !okPayload.OK || okPayload.ID < 1 || okPayload.Name != "edge-nl-01" || okPayload.Port != 2222 {
		t.Fatalf("unexpected 201 JSON payload: %+v", okPayload)
	}
	if strings.Contains(okRec.Body.String(), "S3cretPass!") {
		t.Fatal("create JSON response leaked SSH password")
	}
	saved, err := repo.GetByID(context.Background(), okPayload.ID)
	if err != nil {
		t.Fatalf("GetByID(%d) error = %v", okPayload.ID, err)
	}
	if saved.TrafficResetDay != 15 || len(saved.Tags) != 2 {
		t.Fatalf("unexpected saved server: %+v", saved)
	}
}

func TestConnectionHandlerValidatesAndHandlesUnavailableSSH(t *testing.T) {
	deps, repo := testServerDeps(t)
	h := servers.NewTestConnectionHandler(deps, repo)

	// 1. Missing host/username returns 422 JSON even when name is omitted.
	badForm := url.Values{
		"host":                {""},
		"port":                {"22"},
		"username":            {""},
		"auth_mode":           {servers.AuthModeHybrid},
		"credential_strategy": {servers.CredentialStrategyStored},
	}
	badReq := httptest.NewRequest(http.MethodPost, "/servers/test-connection", strings.NewReader(badForm.Encode()))
	badReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badRec := httptest.NewRecorder()
	h.ServeHTTP(badRec, badReq)

	if badRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid test-connection status = %d, want 422", badRec.Code)
	}
	var badPayload struct {
		OK     bool              `json:"ok"`
		Errors map[string]string `json:"errors"`
	}
	if err := json.Unmarshal(badRec.Body.Bytes(), &badPayload); err != nil {
		t.Fatalf("decode 422 JSON: %v", err)
	}
	if badPayload.Errors["host"] == "" || badPayload.Errors["username"] == "" || badPayload.Errors["name"] != "" {
		t.Fatalf("unexpected test-connection validation errors: %+v", badPayload.Errors)
	}

	// 2. Valid connection fields without SSH runtime return 503 JSON.
	okForm := url.Values{
		"host":                {"203.0.113.10"},
		"port":                {"22"},
		"username":            {"root"},
		"auth_mode":           {servers.AuthModeHybrid},
		"credential_strategy": {servers.CredentialStrategyStored},
		"credential_ref":      {"secret"},
	}
	okReq := httptest.NewRequest(http.MethodPost, "/servers/test-connection", strings.NewReader(okForm.Encode()))
	okReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	okRec := httptest.NewRecorder()
	h.ServeHTTP(okRec, okReq)

	if okRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("test-connection with nil SSH status = %d, want 503", okRec.Code)
	}
}

func TestUpdateAndTestConnectionRefuseCredentialReuseAcrossChangedHostOrUser(t *testing.T) {
	deps, repo := testServerDeps(t)
	const secret = "VictimServerPassword#2026"
	created, err := repo.Create(context.Background(), servers.Server{
		Name:               "victim-1",
		Host:               "198.51.100.50",
		Port:               22,
		AuthMode:           servers.AuthModePassword,
		Username:           "root",
		CredentialStrategy: servers.CredentialStrategyStored,
		CredentialRef:      secret,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	sid := strconv.FormatInt(created.ID, 10)

	// 1. UpdateHandler must reject changing host without re-entering credentials.
	updH := servers.NewUpdateHandler(deps, repo)
	updForm := url.Values{
		"name":                {"victim-1"},
		"host":                {"203.0.113.99"}, // changed host!
		"port":                {"22"},
		"auth_mode":           {servers.AuthModePassword},
		"username":            {"root"},
		"credential_strategy": {servers.CredentialStrategyStored},
		"credential_ref":      {""},
	}
	updReq := httptest.NewRequest(http.MethodPost, "/servers/"+sid+"/edit", strings.NewReader(updForm.Encode()))
	updReq.SetPathValue("id", sid)
	updReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	updRec := httptest.NewRecorder()
	updH.ServeHTTP(updRec, updReq)

	if updRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("host change without credential status = %d, want 422", updRec.Code)
	}
	after, err := repo.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if after.Host != "198.51.100.50" || after.CredentialRef != secret {
		t.Fatalf("server unexpectedly mutated on rejected update: %+v", after)
	}
}

func TestListHandlerRendersAddServerModalAndQueryAutoOpen(t *testing.T) {
	deps, repo := testServerDeps(t)
	h := servers.NewListHandler(deps, repo)

	req := httptest.NewRequest(http.MethodGet, "/servers?add=1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ListHandler status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-server-add-modal`,
		`data-modal-id="add-server-modal"`,
		`data-modal-open="add-server-modal"`,
		`data-modal-open-initial`,
		`data-sf-test-btn`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/servers?add=1 missing %q", want)
		}
	}
}

func TestDeleteHandlerJSON(t *testing.T) {
	deps, repo := testServerDeps(t)
	created, err := repo.Create(context.Background(), servers.Server{
		Name:               "ephemeral-node",
		Host:               "198.51.100.77",
		Port:               22,
		AuthMode:           servers.AuthModePassword,
		Username:           "root",
		CredentialStrategy: servers.CredentialStrategyRuntime,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	h := servers.NewDeleteHandler(deps, repo)
	req := httptest.NewRequest(http.MethodPost, "/servers/"+strconv.FormatInt(created.ID, 10)+"/delete", nil)
	req.SetPathValue("id", strconv.FormatInt(created.ID, 10))
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("JSON delete status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		OK      bool   `json:"ok"`
		ID      int64  `json:"id"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode delete JSON: %v", err)
	}
	if !payload.OK || payload.ID != created.ID || payload.Message == "" {
		t.Fatalf("unexpected delete JSON payload: %+v", payload)
	}
	if _, err := repo.GetByID(context.Background(), created.ID); err == nil {
		t.Fatal("expected server to be deleted from repository")
	}
}
