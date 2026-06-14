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

// newBulkMux registers POST + job-page handlers (with a fake runner) on a mux
// so PathValue routing works exactly as in production.
func newBulkMux(t *testing.T, deps module.Dependencies, serverRepo servers.Repository, runner *fakeRunner) *http.ServeMux {
	t.Helper()
	action, page := bulk.NewTestHandlers(deps, serverRepo, runner)
	mux := http.NewServeMux()
	mux.Handle("POST /servers/bulk", action)
	mux.Handle("GET /servers/bulk/jobs/{job}", page)
	return mux
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

func postBulk(t *testing.T, mux *http.ServeMux, action string, ids []int64) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"action": {action}}
	for _, id := range ids {
		form.Add("server_ids", strconv.FormatInt(id, 10))
	}
	form.Set("_csrf_token", "test") // middleware is bypassed in unit tests
	req := httptest.NewRequest(http.MethodPost, "/servers/bulk", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// startJob posts the bulk form and returns the redirect target (the job page).
func startJob(t *testing.T, mux *http.ServeMux, action string, ids []int64) string {
	t.Helper()
	w := postBulk(t, mux, action, ids)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST status = %d, want 303", w.Code)
	}
	location := w.Header().Get("Location")
	if !strings.HasPrefix(location, "/servers/bulk/jobs/") {
		t.Fatalf("redirect = %q, want a /servers/bulk/jobs/ URL", location)
	}
	return location
}

// waitForJob polls the job page until the background run finishes and returns
// the final page body.
func waitForJob(t *testing.T, mux *http.ServeMux, jobPath string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		req := httptest.NewRequest(http.MethodGet, jobPath, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", jobPath, w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "data-bulk-sse-url") {
			return body // finished: no live-stream attribute rendered
		}
		if time.Now().After(deadline) {
			t.Fatal("bulk job did not finish within 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestBulkActionValidation(t *testing.T) {
	deps := newDeps(t)
	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	mux := newBulkMux(t, deps, serverRepo, &fakeRunner{})

	t.Run("invalid action redirects to servers", func(t *testing.T) {
		w := postBulk(t, mux, "launch-missiles", []int64{1})
		if w.Code != http.StatusSeeOther {
			t.Errorf("got status %d, want %d", w.Code, http.StatusSeeOther)
		}
		if loc := w.Header().Get("Location"); !strings.Contains(loc, "bulk-invalid-action") {
			t.Errorf("redirect = %q, want bulk-invalid-action flash", loc)
		}
	})

	t.Run("empty selection redirects to servers", func(t *testing.T) {
		w := postBulk(t, mux, "reboot", nil)
		if w.Code != http.StatusSeeOther {
			t.Errorf("got status %d, want %d", w.Code, http.StatusSeeOther)
		}
		if loc := w.Header().Get("Location"); !strings.Contains(loc, "bulk-no-selection") {
			t.Errorf("redirect = %q, want bulk-no-selection flash", loc)
		}
	})

	t.Run("non-numeric ids are silently dropped", func(t *testing.T) {
		form := url.Values{
			"action":     {"reboot"},
			"server_ids": {"abc", "0", "-1"},
		}
		req := httptest.NewRequest(http.MethodPost, "/servers/bulk", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		// All ids invalid → no-selection redirect, not a job.
		if w.Code != http.StatusSeeOther {
			t.Errorf("got status %d, want %d", w.Code, http.StatusSeeOther)
		}
		if loc := w.Header().Get("Location"); !strings.Contains(loc, "bulk-no-selection") {
			t.Errorf("redirect = %q, want bulk-no-selection flash", loc)
		}
	})
}

func TestBulkJobPageUnknownJobRedirects(t *testing.T) {
	deps := newDeps(t)
	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	mux := newBulkMux(t, deps, serverRepo, &fakeRunner{})

	req := httptest.NewRequest(http.MethodGet, "/servers/bulk/jobs/no-such-job", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("got status %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "bulk-job-expired") {
		t.Errorf("redirect = %q, want bulk-job-expired flash", loc)
	}
}

func TestBulkDeleteLoopsOverIDs(t *testing.T) {
	deps := newDeps(t)
	serverRepo := servers.NewSQLRepository(deps.Database.SQL)

	s1 := seedServer(t, serverRepo, false)
	s2 := seedServer(t, serverRepo, false)

	mux := newBulkMux(t, deps, serverRepo, &fakeRunner{})
	jobPath := startJob(t, mux, "delete", []int64{s1.ID, s2.ID})
	body := waitForJob(t, mux, jobPath)

	for _, id := range []int64{s1.ID, s2.ID} {
		_, err := serverRepo.GetByID(context.Background(), id)
		if err == nil {
			t.Errorf("server %d still exists after bulk delete", id)
		}
	}
	if !strings.Contains(body, "2 ok") {
		t.Error("expected '2 ok' summary on the finished job page")
	}
}

func TestBulkSkipsNoCreds(t *testing.T) {
	deps := newDeps(t)
	serverRepo := servers.NewSQLRepository(deps.Database.SQL)

	withCreds := seedServer(t, serverRepo, true)
	noCreds := seedServer(t, serverRepo, false)

	mux := newBulkMux(t, deps, serverRepo, &fakeRunner{})
	jobPath := startJob(t, mux, "reboot", []int64{withCreds.ID, noCreds.ID})
	body := waitForJob(t, mux, jobPath)

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
	mux := newBulkMux(t, deps, serverRepo, runner)
	jobPath := startJob(t, mux, "reboot", ids)
	waitForJob(t, mux, jobPath)

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
			mux := newBulkMux(t, deps, serverRepo, runner)
			jobPath := startJob(t, mux, "update", []int64{s.ID})
			body := waitForJob(t, mux, jobPath)

			if !strings.Contains(body, tc.wantText) {
				t.Errorf("finished job page does not contain %q", tc.wantText)
			}
		})
	}
}
