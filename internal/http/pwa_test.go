package webhttp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	assets "github.com/Ho3einK84/Nodexia"
	"github.com/Ho3einK84/Nodexia/internal/commandstream"
	webhttp "github.com/Ho3einK84/Nodexia/internal/http"
	"github.com/Ho3einK84/Nodexia/internal/logging"
	"github.com/Ho3einK84/Nodexia/internal/module/registry"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

func newPWATestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := testutil.TestConfig(t)
	logging.Setup(cfg.Log)

	runtime := testutil.OpenTestDB(t)
	renderer, err := view.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	staticFiles, err := assets.Static()
	if err != nil {
		t.Fatalf("Static() error = %v", err)
	}

	handler := webhttp.NewRouter(
		cfg,
		runtime,
		sshclient.New(cfg.SSH, cfg.Security),
		commandstream.New(0),
		nil, nil,
		renderer,
		staticFiles,
		nil,
		registry.DefaultModules(),
	)

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

// TestManifestServedPublicly verifies the manifest is reachable without auth and
// describes an installable, standalone app.
func TestManifestServedPublicly(t *testing.T) {
	server := newPWATestServer(t)

	resp := mustGet(t, server.URL+"/manifest.webmanifest")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (manifest must be public)", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "manifest+json") {
		t.Fatalf("Content-Type = %q, want manifest+json", ct)
	}

	var manifest struct {
		Name     string `json:"name"`
		StartURL string `json:"start_url"`
		Scope    string `json:"scope"`
		Display  string `json:"display"`
		Icons    []struct {
			Src     string `json:"src"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.StartURL != "/" || manifest.Scope != "/" {
		t.Fatalf("start_url=%q scope=%q, want both \"/\"", manifest.StartURL, manifest.Scope)
	}
	if manifest.Display != "standalone" {
		t.Fatalf("display = %q, want standalone", manifest.Display)
	}
	if manifest.Name == "" {
		t.Fatal("manifest name is empty")
	}

	var hasMaskable, hasAny bool
	for _, icon := range manifest.Icons {
		if icon.Purpose == "maskable" {
			hasMaskable = true
		}
		if icon.Purpose == "any" {
			hasAny = true
		}
	}
	if !hasMaskable || !hasAny {
		t.Fatalf("icons missing purposes: any=%v maskable=%v", hasAny, hasMaskable)
	}
}

// TestManifestShortcutsHaveIcons verifies every manifest shortcut declares a
// target URL and its own icon at the Android-baseline 96x96 size. A shortcut
// without icons renders a blank placeholder in the launcher (the bug this guards
// against), so the icons array is required, not optional.
func TestManifestShortcutsHaveIcons(t *testing.T) {
	server := newPWATestServer(t)

	resp := mustGet(t, server.URL+"/manifest.webmanifest")
	defer resp.Body.Close()

	var manifest struct {
		Shortcuts []struct {
			Name  string `json:"name"`
			URL   string `json:"url"`
			Icons []struct {
				Src   string `json:"src"`
				Sizes string `json:"sizes"`
				Type  string `json:"type"`
			} `json:"icons"`
		} `json:"shortcuts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}

	if len(manifest.Shortcuts) == 0 {
		t.Fatal("manifest declares no shortcuts")
	}

	for _, sc := range manifest.Shortcuts {
		if sc.Name == "" || sc.URL == "" {
			t.Fatalf("shortcut missing name/url: %+v", sc)
		}
		var has96 bool
		for _, icon := range sc.Icons {
			if icon.Src == "" || icon.Type != "image/png" {
				t.Fatalf("shortcut %q has malformed icon: %+v", sc.Name, icon)
			}
			if icon.Sizes == "96x96" {
				has96 = true
				// The referenced icon must actually be served.
				iconResp := mustGet(t, server.URL+icon.Src)
				iconResp.Body.Close()
				if iconResp.StatusCode != http.StatusOK {
					t.Fatalf("shortcut %q icon %s = %d, want 200", sc.Name, icon.Src, iconResp.StatusCode)
				}
			}
		}
		if !has96 {
			t.Fatalf("shortcut %q has no 96x96 icon", sc.Name)
		}
	}
}

// TestShortcutTargetsResolveCleanly verifies each shortcut URL resolves to a real
// route that, when launched cold (unauthenticated), cleanly redirects to /login
// rather than 404ing or rendering a blank screen.
func TestShortcutTargetsResolveCleanly(t *testing.T) {
	server := newPWATestServer(t)

	// A client that captures redirects instead of following them, so we can
	// assert the auth bounce is a clean 303 → /login.
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	for _, url := range []string{"/servers", "/ops/diagnostics", "/alerts"} {
		resp, err := client.Get(server.URL + url)
		if err != nil {
			t.Fatalf("GET %s error = %v", url, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("GET %s = %d, want 303 (clean auth redirect)", url, resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); loc != "/login" {
			t.Fatalf("GET %s Location = %q, want /login", url, loc)
		}
	}
}

// TestServiceWorkerServedFromRoot verifies the worker is public, has a JS content
// type, and advertises root scope so it can control the whole origin.
func TestServiceWorkerServedFromRoot(t *testing.T) {
	server := newPWATestServer(t)

	resp := mustGet(t, server.URL+"/sw.js")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("Content-Type = %q, want javascript", ct)
	}
	if allowed := resp.Header.Get("Service-Worker-Allowed"); allowed != "/" {
		t.Fatalf("Service-Worker-Allowed = %q, want /", allowed)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "addEventListener") {
		t.Fatal("service worker body does not look like a worker script")
	}
}

// TestPWAAssetsPresent guards that the static PWA assets the manifest and worker
// reference are actually embedded and served.
func TestPWAAssetsPresent(t *testing.T) {
	server := newPWATestServer(t)

	for _, path := range []string{
		"/static/icon-192.png",
		"/static/icon-512.png",
		"/static/icon-maskable-512.png",
		"/static/apple-touch-icon.png",
		"/static/shortcut-servers.png",
		"/static/shortcut-diagnostics.png",
		"/static/shortcut-alerts.png",
		"/static/favicon.svg",
		"/static/offline.html",
		"/static/sw.js",
	} {
		resp := mustGet(t, server.URL+path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, resp.StatusCode)
		}
	}
}
