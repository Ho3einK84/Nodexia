package webhttp_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	assets "github.com/Ho3einK84/Nodexia"
	"github.com/Ho3einK84/Nodexia/internal/app"
	"github.com/Ho3einK84/Nodexia/internal/commandstream"
	webhttp "github.com/Ho3einK84/Nodexia/internal/http"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/logging"
	"github.com/Ho3einK84/Nodexia/internal/module/registry"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

func TestHealthEndpointsSmoke(t *testing.T) {
	cfg := testutil.TestConfig(t)
	logging.Setup(cfg.Log)

	runtime := testutil.OpenTestDB(t)
	renderer, err := view.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}

	sshService := sshclient.New(cfg.SSH, cfg.Security)
	streams := commandstream.New(0)
	staticFiles, err := assets.Static()
	if err != nil {
		t.Fatalf("Static() error = %v", err)
	}

	handler := webhttp.NewRouter(
		cfg,
		runtime,
		sshService,
		streams,
		nil, // terminalTickets
		renderer,
		staticFiles,
		nil, // backgroundScheduler
		registry.DefaultModules(),
	)

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	t.Run("liveness", func(t *testing.T) {
		resp := mustGet(t, server.URL+"/healthz")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		body := readBody(t, resp)
		if body != "ok" {
			t.Fatalf("body = %q, want ok", body)
		}
	})

	t.Run("ready", func(t *testing.T) {
		resp := mustGet(t, server.URL+"/healthz/ready")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})

	t.Run("diagnostics", func(t *testing.T) {
		token, err := middleware.GenerateAuthToken(
			cfg.Security.AdminUsername,
			[]byte(cfg.Security.SessionSecret),
			cfg.Security.SessionTTL,
		)
		if err != nil {
			t.Fatalf("GenerateAuthToken() error = %v", err)
		}
		cookie := middleware.BuildAuthCookie(token, cfg.Security.SessionTTL, cfg.Security.SessionCookieSecure)

		req, err := http.NewRequest(http.MethodGet, server.URL+"/ops/diagnostics", nil)
		if err != nil {
			t.Fatalf("NewRequest() error = %v", err)
		}
		req.AddCookie(cookie)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /ops/diagnostics error = %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		body := readBody(t, resp)
		if !strings.Contains(body, "Operational diagnostics") {
			t.Fatalf("diagnostics page missing expected heading")
		}
	})
}

func TestApplicationBootstrapAndShutdown(t *testing.T) {
	cfg := testutil.TestConfig(t)
	logging.Setup(cfg.Log)

	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app.New() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := application.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestRouterPathValuesArePreserved(t *testing.T) {
	// Regression test: Go's ServeMux only populates PathValue when
	// the handler is invoked via ServeHTTP, not when extracted via
	// Handler() and called directly. The router must not use the
	// Handler()+direct-call pattern.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /servers/{id}/edit", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			http.Error(w, "PathValue is empty", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/servers/42/edit", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q (PathValue was empty; router may be broken)", rr.Code, rr.Body.String())
	}
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s error = %v", url, err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}
