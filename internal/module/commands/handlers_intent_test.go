package commands_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/commandstream"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/commands"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

func newTestMux(t *testing.T) (*http.ServeMux, servers.Repository) {
	t.Helper()
	runtime := testutil.OpenTestDB(t)
	cfg := testutil.TestConfig(t)
	renderer, err := view.NewRenderer()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}
	deps := module.Dependencies{
		Config:         cfg,
		Database:       runtime,
		SSH:            sshclient.New(cfg.SSH, cfg.Security),
		CommandStreams: commandstream.New(0),
		Renderer:       renderer,
	}
	mux := http.NewServeMux()
	commands.New().RegisterRoutes(mux, deps)
	return mux, servers.NewSQLRepository(runtime.SQL)
}

func seedStoredCredServer(t *testing.T, repo servers.Repository) servers.Server {
	t.Helper()
	s, err := repo.Create(context.Background(), servers.Server{
		Name:               "cmd-test-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Host:               "203.0.113.1", // TEST-NET-3: never routable
		Port:               22,
		AuthMode:           servers.AuthModePassword,
		Username:           "root",
		CredentialStrategy: servers.CredentialStrategyStored,
		CredentialRef:      "secret",
	})
	if err != nil {
		t.Fatalf("seed server: %v", err)
	}
	return s
}

func postCommand(t *testing.T, mux *http.ServeMux, serverID int64, intent, command string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{
		"_csrf_token": {"test"},
		"intent":      {intent},
		"command":     {command},
	}
	path := "/servers/" + strconv.FormatInt(serverID, 10) + "/commands"
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestTerminalIntentRedirectsToTerminal(t *testing.T) {
	mux, repo := newTestMux(t)
	s := seedStoredCredServer(t, repo)

	w := postCommand(t, mux, s.ID, "terminal", "ls -la /var/log")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	loc := w.Header().Get("Location")
	want := "/servers/" + strconv.FormatInt(s.ID, 10) + "/terminal?init="
	if !strings.HasPrefix(loc, want) {
		t.Errorf("redirect = %q, want prefix %q", loc, want)
	}
}

func TestRunIntentRedirectsInteractiveCommandToTerminal(t *testing.T) {
	mux, repo := newTestMux(t)
	s := seedStoredCredServer(t, repo)

	w := postCommand(t, mux, s.ID, "run", "htop")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "/terminal?init=htop") {
		t.Errorf("redirect = %q, want terminal init URL", loc)
	}
}

func TestRunIntentStartsBackgroundStream(t *testing.T) {
	mux, repo := newTestMux(t)
	s := seedStoredCredServer(t, repo)

	// "Run" must redirect to the live stream page immediately instead of
	// holding the request open for the duration of the command (502 source).
	w := postCommand(t, mux, s.ID, "run", "uname -a")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "/commands?stream=") {
		t.Errorf("redirect = %q, want a /commands?stream= URL", loc)
	}
}

func TestTerminalIntentRequiresCommand(t *testing.T) {
	mux, repo := newTestMux(t)
	s := seedStoredCredServer(t, repo)

	w := postCommand(t, mux, s.ID, "terminal", "   ")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 validation page", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Enter a shell command") {
		t.Error("expected command-required validation message")
	}
}
