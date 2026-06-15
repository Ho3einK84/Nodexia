package handlers

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/config"
)

// PWA assets (the web app manifest and the service worker) are served from
// dedicated routes rather than from /static so they can carry the exact headers
// and scope a Progressive Web App requires:
//
//   - The manifest reflects the configured App.Name and is reachable without
//     authentication (browsers fetch it without credentials).
//   - The service worker is served from the site root with
//     "Service-Worker-Allowed: /" so it controls the whole origin, and with
//     "Cache-Control: no-cache" so update checks are prompt.

// ManifestHandler renders the web app manifest from the running configuration.
type ManifestHandler struct {
	body []byte
}

type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose,omitempty"`
}

// manifestShortcut mirrors a Web App Manifest "shortcuts" entry. Each shortcut
// declares its own icons array: without one the launcher renders a blank
// placeholder (Android prefers a 96x96 shortcut icon), so per-shortcut icons are
// required, not optional. ShortName/Description improve the long-press menu.
type manifestShortcut struct {
	Name        string         `json:"name"`
	ShortName   string         `json:"short_name"`
	Description string         `json:"description"`
	URL         string         `json:"url"`
	Icons       []manifestIcon `json:"icons"`
}

type webManifest struct {
	Name        string `json:"name"`
	ShortName   string `json:"short_name"`
	Description string `json:"description"`
	ID          string `json:"id"`
	StartURL    string `json:"start_url"`
	Scope       string `json:"scope"`
	Display     string `json:"display"`
	// Orientation is deliberately omitted. Declaring an explicit value (notably
	// "any") makes an installed PWA request an orientation lock from the OS,
	// which overrides the user's system rotation lock and force-rotates the app
	// even when they have locked portrait. With no orientation member the
	// platform's auto-rotate / rotation-lock setting governs the app: locked
	// portrait stays portrait, and an unlocked device is free to rotate (so the
	// SSH terminal can still be used in landscape when the user allows it).
	ThemeColor      string             `json:"theme_color"`
	BackgroundColor string             `json:"background_color"`
	Lang            string             `json:"lang"`
	Dir             string             `json:"dir"`
	Categories      []string           `json:"categories"`
	Icons           []manifestIcon     `json:"icons"`
	Shortcuts       []manifestShortcut `json:"shortcuts"`
}

// NewManifestHandler builds the manifest once at startup; it depends only on
// static configuration, so there is no need to re-encode it per request.
func NewManifestHandler(cfg config.Config) ManifestHandler {
	name := strings.TrimSpace(cfg.App.Name)
	if name == "" {
		name = "Nodexia"
	}

	manifest := webManifest{
		Name:            name,
		ShortName:       name,
		Description:     "Self-hosted control panel for monitoring and managing Rebecca and PasarGuard panel nodes.",
		ID:              "/",
		StartURL:        "/",
		Scope:           "/",
		Display:         "standalone",
		ThemeColor:      "#0f172a",
		BackgroundColor: "#0f172a",
		Lang:            "en",
		Dir:             "ltr",
		Categories:      []string{"productivity", "utilities"},
		Icons: []manifestIcon{
			{Src: "/static/icon-192.png", Sizes: "192x192", Type: "image/png", Purpose: "any"},
			{Src: "/static/icon-512.png", Sizes: "512x512", Type: "image/png", Purpose: "any"},
			{Src: "/static/icon-maskable-512.png", Sizes: "512x512", Type: "image/png", Purpose: "maskable"},
		},
		Shortcuts: []manifestShortcut{
			{
				Name:        "Servers",
				ShortName:   "Servers",
				Description: "Browse the managed server registry.",
				URL:         "/servers",
				Icons: []manifestIcon{
					{Src: "/static/shortcut-servers.png", Sizes: "96x96", Type: "image/png", Purpose: "any"},
				},
			},
			{
				Name:        "Diagnostics",
				ShortName:   "Diagnostics",
				Description: "Check system health and background jobs.",
				URL:         "/ops/diagnostics",
				Icons: []manifestIcon{
					{Src: "/static/shortcut-diagnostics.png", Sizes: "96x96", Type: "image/png", Purpose: "any"},
				},
			},
			{
				Name:        "Alerts",
				ShortName:   "Alerts",
				Description: "Review alert rules, channels, and events.",
				URL:         "/alerts",
				Icons: []manifestIcon{
					{Src: "/static/shortcut-alerts.png", Sizes: "96x96", Type: "image/png", Purpose: "any"},
				},
			},
		},
	}

	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		// The manifest is built from static, known-good values; a marshal error
		// here is a programming error. Fall back to an empty object so the route
		// still responds rather than panicking at startup.
		body = []byte("{}")
	}

	return ManifestHandler{body: body}
}

func (h ManifestHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(h.body)
}

// ServiceWorkerHandler serves the embedded service worker script from the site
// root so its control scope is the whole origin.
type ServiceWorkerHandler struct {
	body    []byte
	modTime time.Time
}

// NewServiceWorkerHandler reads the worker source out of the static file system
// once at startup. staticFiles is the same fs.FS the /static/ route serves, so
// the worker stays a single source file under web/static.
func NewServiceWorkerHandler(staticFiles fs.FS) ServiceWorkerHandler {
	body, err := fs.ReadFile(staticFiles, "sw.js")
	if err != nil {
		// Missing worker is non-fatal: serve an empty script so registration
		// fails quietly on the client instead of taking the server down.
		body = []byte("/* service worker unavailable */\n")
	}
	return ServiceWorkerHandler{body: body, modTime: time.Now()}
}

func (h ServiceWorkerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	// Let the browser re-check for an updated worker on every navigation while
	// still allowing a conditional (304) response.
	w.Header().Set("Cache-Control", "no-cache")
	// Permit the root scope even though the script is served from /sw.js.
	w.Header().Set("Service-Worker-Allowed", "/")
	http.ServeContent(w, r, "sw.js", h.modTime, bytes.NewReader(h.body))
}
