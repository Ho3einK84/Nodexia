package bulk_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/bulk"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

// ── Fakes ────────────────────────────────────────────────────────────────────

type fakeRunner struct {
	current     int64 // accessed atomically
	maxObserved int64 // accessed atomically
	exitCode    int
	runErr      error
	delay       time.Duration
}

func (f *fakeRunner) RunCommand(ctx context.Context, req sshclient.CommandRequest) (sshclient.CommandResult, error) {
	c := atomic.AddInt64(&f.current, 1)
	for {
		m := atomic.LoadInt64(&f.maxObserved)
		if c <= m || atomic.CompareAndSwapInt64(&f.maxObserved, m, c) {
			break
		}
	}
	defer atomic.AddInt64(&f.current, -1)

	if f.delay > 0 {
		time.Sleep(f.delay)
	}

	if f.runErr != nil {
		return sshclient.CommandResult{}, f.runErr
	}
	code := f.exitCode
	return sshclient.CommandResult{ExitCode: &code}, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func newDeps(t *testing.T) module.Dependencies {
	t.Helper()
	runtime := testutil.OpenTestDB(t)
	cfg := testutil.TestConfig(t)
	renderer, err := view.NewRenderer()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}
	return module.Dependencies{
		Config:   cfg,
		Database: runtime,
		Renderer: renderer,
	}
}

func seedServer(t *testing.T, repo servers.Repository, hasCreds bool) servers.Server {
	t.Helper()
	cred := servers.CredentialStrategyRuntime
	ref := ""
	if hasCreds {
		cred = servers.CredentialStrategyStored
		ref = "secret-password"
	}
	s, err := repo.Create(context.Background(), servers.Server{
		Name:               "srv-" + fmt.Sprintf("%d", time.Now().UnixNano()),
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

func postBulk(t *testing.T, handler http.Handler, action string, ids []int64) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"action": {action}}
	for _, id := range ids {
		form.Add("server_ids", strconv.FormatInt(id, 10))
	}
	// Fake CSRF: the test bypasses middleware, so token is ignored.
	form.Set("_csrf_token", "test")
	req := httptest.NewRequest(http.MethodPost, "/servers/bulk", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Inject fake session/csrf into context so middleware helpers don't panic.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestBulkActionValidation(t *testing.T) {
	deps := newDeps(t)
	serverRepo := servers.NewSQLRepository(deps.Database.SQL)

	t.Run("invalid action redirects", func(t *testing.T) {
		h := bulk.NewActionHandler(deps, serverRepo)
		w := postBulk(t, h, "launch-missiles", []int64{1})
		if w.Code != http.StatusSeeOther {
			t.Errorf("got status %d, want %d", w.Code, http.StatusSeeOther)
		}
	})

	t.Run("empty selection redirects", func(t *testing.T) {
		h := bulk.NewActionHandler(deps, serverRepo)
		w := postBulk(t, h, "reboot", nil)
		if w.Code != http.StatusSeeOther {
			t.Errorf("got status %d, want %d", w.Code, http.StatusSeeOther)
		}
	})

	t.Run("non-numeric ids are silently dropped", func(t *testing.T) {
		h := bulk.NewActionHandler(deps, serverRepo)
		form := url.Values{
			"action":     {"reboot"},
			"server_ids": {"abc", "0", "-1"},
		}
		req := httptest.NewRequest(http.MethodPost, "/servers/bulk", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		// All ids invalid → redirect (no-selection path).
		if w.Code != http.StatusSeeOther {
			t.Errorf("got status %d, want %d", w.Code, http.StatusSeeOther)
		}
	})
}

func TestBulkDeleteLoopsOverIDs(t *testing.T) {
	deps := newDeps(t)
	serverRepo := servers.NewSQLRepository(deps.Database.SQL)

	s1 := seedServer(t, serverRepo, false)
	s2 := seedServer(t, serverRepo, false)

	h := bulk.NewActionHandler(deps, serverRepo)
	w := postBulk(t, h, "delete", []int64{s1.ID, s2.ID})

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}

	// Both servers should be gone.
	for _, id := range []int64{s1.ID, s2.ID} {
		_, err := serverRepo.GetByID(context.Background(), id)
		if err == nil {
			t.Errorf("server %d still exists after bulk delete", id)
		}
	}
}

func TestBulkSkipsNoCreds(t *testing.T) {
	deps := newDeps(t)
	serverRepo := servers.NewSQLRepository(deps.Database.SQL)

	withCreds := seedServer(t, serverRepo, true)
	noCreds := seedServer(t, serverRepo, false)

	runner := &fakeRunner{}
	h := bulk.NewActionHandlerWithRunner(deps, serverRepo, runner)

	w := postBulk(t, h, "reboot", []int64{withCreds.ID, noCreds.ID})
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "skipped") {
		t.Error("expected 'skipped' in page body for no-creds server")
	}
	if !strings.Contains(body, "no stored credentials") {
		t.Error("expected 'no stored credentials' reason in page body")
	}
}

func TestBulkWorkerPoolCapsConcurrency(t *testing.T) {
	deps := newDeps(t)
	serverRepo := servers.NewSQLRepository(deps.Database.SQL)

	// Seed more servers than bulkWorkers.
	const n = 12
	ids := make([]int64, n)
	for i := range ids {
		s := seedServer(t, serverRepo, true)
		ids[i] = s.ID
	}

	runner := &fakeRunner{delay: 20 * time.Millisecond}
	h := bulk.NewActionHandlerWithRunner(deps, serverRepo, runner)

	w := postBulk(t, h, "reboot", ids)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}

	max := atomic.LoadInt64(&runner.maxObserved)
	if max > bulk.BulkWorkers {
		t.Errorf("max concurrent SSH calls = %d, want <= %d", max, bulk.BulkWorkers)
	}
}

func TestBulkExitCodeMapping(t *testing.T) {
	cases := []struct {
		name     string
		exitCode int
		wantText string
	}{
		{"sudo-password", 88, "sudo requires password"},
		{"unsupported-pkg", 87, "unsupported system"},
		{"generic-failure", 1, "exit 1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := newDeps(t)
			serverRepo := servers.NewSQLRepository(deps.Database.SQL)
			s := seedServer(t, serverRepo, true)

			runner := &fakeRunner{exitCode: tc.exitCode}
			h := bulk.NewActionHandlerWithRunner(deps, serverRepo, runner)

			w := postBulk(t, h, "update", []int64{s.ID})
			if w.Code != http.StatusOK {
				t.Fatalf("got status %d, want 200", w.Code)
			}

			body := w.Body.String()
			if !strings.Contains(body, tc.wantText) {
				t.Errorf("body does not contain %q\nbody snippet: %s", tc.wantText, body[:min(300, len(body))])
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
