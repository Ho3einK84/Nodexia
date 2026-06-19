package terminal_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/module/terminal"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/terminalticket"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

func newTestDeps(t *testing.T) module.Dependencies {
	t.Helper()
	runtime := testutil.OpenTestDB(t)
	cfg := testutil.TestConfig(t)
	renderer, err := view.NewRenderer()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}
	// A real *sshclient.Service is required so that RegisterRoutes wires the real
	// handlers (not the placeholder). No SSH connections are made in these tests
	// because all tested paths fail before OpenShell is called.
	ssh := sshclient.New(cfg.SSH, cfg.Security)
	return module.Dependencies{
		Config:          cfg,
		Database:        runtime,
		SSH:             ssh,
		TerminalTickets: terminalticket.New(30 * time.Second),
		Renderer:        renderer,
	}
}

func seedServer(t *testing.T, repo servers.Repository, hasCreds bool) servers.Server {
	t.Helper()
	cred, ref := servers.CredentialStrategyRuntime, ""
	if hasCreds {
		cred = servers.CredentialStrategyStored
		ref = "secret"
	}
	s, err := repo.Create(context.Background(), servers.Server{
		Name:               "test-server",
		Host:               "10.0.0.1",
		Port:               22,
		AuthMode:           servers.AuthModePassword,
		Username:           "root",
		CredentialStrategy: cred,
		CredentialRef:      ref,
	})
	if err != nil {
		t.Fatalf("seed server: %v", err)
	}
	return s
}

func registerRoutes(t *testing.T, deps module.Dependencies) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	m := terminal.New()
	m.RegisterRoutes(mux, deps)
	return mux
}

func sid(id int64) string {
	return strconv.FormatInt(id, 10)
}

// sameOriginRequest sets Origin and Host to the same value so that
// ValidateSameOriginRequest passes in unit tests.
func sameOriginRequest(method, path string, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	r.Header.Set("Origin", "http://example.com")
	r.Host = "example.com"
	return r
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestTerminalGETShowsForm(t *testing.T) {
	deps := newTestDeps(t)
	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	s := seedServer(t, serverRepo, false)

	mux := registerRoutes(t, deps)
	req := httptest.NewRequest(http.MethodGet, "/servers/"+sid(s.ID)+"/terminal", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET terminal: status %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(strings.ToLower(body), "terminal") {
		t.Error("response body should reference 'terminal'")
	}
}

func TestTerminalPOSTValidationRejectsEmptyCreds(t *testing.T) {
	deps := newTestDeps(t)
	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	s := seedServer(t, serverRepo, false) // no stored creds

	mux := registerRoutes(t, deps)

	form := url.Values{
		"_csrf_token": {"test"},
		"password":    {""},
	}
	req := sameOriginRequest(http.MethodPost,
		"/servers/"+sid(s.ID)+"/terminal",
		form.Encode())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", w.Code)
	}
	// Validation error: page must not contain a terminal ticket.
	if strings.Contains(w.Body.String(), "data-ticket") {
		t.Error("validation-failed POST should not produce a ticket page")
	}
}

func TestTerminalPOSTStoredCredsCreatesTicket(t *testing.T) {
	deps := newTestDeps(t)
	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	s := seedServer(t, serverRepo, true) // has stored creds

	mux := registerRoutes(t, deps)

	form := url.Values{"_csrf_token": {"test"}}
	req := sameOriginRequest(http.MethodPost,
		"/servers/"+sid(s.ID)+"/terminal",
		form.Encode())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "data-ticket") {
		t.Error("expected data-ticket attribute in response body after stored-cred POST")
	}
}

func TestTerminalWSRejectsCrossOrigin(t *testing.T) {
	deps := newTestDeps(t)
	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	s := seedServer(t, serverRepo, true)

	ticketID := deps.TerminalTickets.Create(s.ID, sshclient.ConnectionRequest{})

	mux := registerRoutes(t, deps)
	req := httptest.NewRequest(http.MethodGet,
		"/servers/"+sid(s.ID)+"/terminal/ws?ticket="+ticketID, nil)
	// Cross-origin: origin host differs from request host.
	req.Header.Set("Origin", "http://evil.example.com")
	req.Host = "good.example.com"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("cross-origin WS: status = %d, want 403", w.Code)
	}
}

func TestTerminalWSRejectsMissingTicket(t *testing.T) {
	deps := newTestDeps(t)
	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	s := seedServer(t, serverRepo, true)

	mux := registerRoutes(t, deps)
	req := sameOriginRequest(http.MethodGet, "/servers/"+sid(s.ID)+"/terminal/ws", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("missing ticket: status = %d, want 400", w.Code)
	}
}

func TestTerminalWSRejectsExpiredTicket(t *testing.T) {
	deps := newTestDeps(t)
	deps.TerminalTickets = terminalticket.New(1 * time.Millisecond)

	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	s := seedServer(t, serverRepo, true)

	ticketID := deps.TerminalTickets.Create(s.ID, sshclient.ConnectionRequest{})
	time.Sleep(5 * time.Millisecond)

	mux := registerRoutes(t, deps)
	req := sameOriginRequest(http.MethodGet,
		"/servers/"+sid(s.ID)+"/terminal/ws?ticket="+ticketID, "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expired ticket: status = %d, want 401", w.Code)
	}
}

func TestTerminalWSSessionLimitRejected(t *testing.T) {
	deps := newTestDeps(t)
	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	s := seedServer(t, serverRepo, true)

	// Pre-fill the session quota for the empty username (what GetAuthenticatedUser
	// returns for requests with no auth cookie, which is the case in unit tests).
	const max = 3
	for i := 0; i < max; i++ {
		deps.TerminalTickets.TryAcquireSession("", max)
	}

	ticketID := deps.TerminalTickets.Create(s.ID, sshclient.ConnectionRequest{})

	mux := registerRoutes(t, deps)
	req := sameOriginRequest(http.MethodGet,
		"/servers/"+sid(s.ID)+"/terminal/ws?ticket="+ticketID, "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("session limit: status = %d, want 429", w.Code)
	}
}
