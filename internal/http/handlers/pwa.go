package handlers

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"time"

	assets "github.com/Ho3einK84/Nodexia"
	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/i18n"
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

// ManifestHandler renders the web app manifest from the running configuration,
// pre-encoding one variant per supported language so dir/lang match the user's
// chosen locale without per-request marshaling.
type ManifestHandler struct {
	bundle   *i18n.Bundle
	byLang   map[string][]byte
	fallback []byte
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
	// Orientation locks the installed PWA to portrait so it never rotates to
	// landscape — not even when the phone is turned sideways with the system
	// rotation lock off. "portrait" (rather than "portrait-primary") is the most
	// broadly honoured value across Android Chrome installs and keeps the app
	// upright. Note: this manifest member is honoured by installed PWAs on
	// Android/Chromium; iOS Safari historically ignores the orientation field, so
	// a runtime screen.orientation.lock('portrait') call backs it up where the
	// platform supports it (see web/static/app.js).
	Orientation     string             `json:"orientation"`
	ThemeColor      string             `json:"theme_color"`
	BackgroundColor string             `json:"background_color"`
	Lang            string             `json:"lang"`
	Dir             string             `json:"dir"`
	Categories      []string           `json:"categories"`
	Icons           []manifestIcon     `json:"icons"`
	Shortcuts       []manifestShortcut `json:"shortcuts"`
}

// NewManifestHandler builds the manifest variants once at startup; they depend
// only on static configuration and locale metadata.
func NewManifestHandler(cfg config.Config) ManifestHandler {
	name := strings.TrimSpace(cfg.App.Name)
	if name == "" {
		name = "Nodexia"
	}

	build := func(lang, dir string) []byte {
		manifest := webManifest{
			Name:            name,
			ShortName:       name,
			Description:     "Self-hosted control panel for monitoring and managing Rebecca and PasarGuard panel nodes.",
			ID:              "/",
			StartURL:        "/",
			Scope:           "/",
			Display:         "standalone",
			Orientation:     "portrait",
			ThemeColor:      "#0f0f10",
			BackgroundColor: "#0f0f10",
			Lang:            lang,
			Dir:             dir,
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
			return []byte("{}")
		}
		return body
	}

	bundle := i18n.MustDefault()
	byLang := make(map[string][]byte)
	for _, lang := range bundle.Languages() {
		byLang[lang.Code] = build(lang.Code, string(lang.Dir))
	}
	fallback := byLang[i18n.DefaultLanguage]
	if len(fallback) == 0 {
		fallback = build("en", "ltr")
	}

	return ManifestHandler{
		bundle:   bundle,
		byLang:   byLang,
		fallback: fallback,
	}
}

func (h ManifestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body := h.fallback
	if h.bundle != nil && r != nil {
		cookieValue := ""
		if cookie, err := r.Cookie(i18n.CookieName); err == nil {
			cookieValue = cookie.Value
		}
		code := h.bundle.Resolve(cookieValue, r.Header.Get("Accept-Language"))
		if localized, ok := h.byLang[code]; ok && len(localized) > 0 {
			body = localized
		}
	}
	w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Add("Vary", "Cookie")
	w.Header().Add("Vary", "Accept-Language")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
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
	} else {
		// Stamp the worker's CACHE_VERSION with the static assets content digest.
		// When static files change or when a new version is released, the SW script
		// body changes, prompting the browser to install the updated worker. On activation,
		// the new worker clears out all prior cache versions so the client never gets stuck
		// with stale CSS/JS assets.
		digest := assets.StaticAssetsDigest()
		body = bytes.Replace(body, []byte("__NODEXIA_CACHE_VERSION__"), []byte("nodexia-"+digest), 1)
		body = bytes.Replace(body, []byte("var CACHE_VERSION = 'v9';"), []byte("var CACHE_VERSION = 'nodexia-"+digest+"';"), 1)
		body = bytes.Replace(body, []byte("var CACHE_VERSION = 'v7';"), []byte("var CACHE_VERSION = 'nodexia-"+digest+"';"), 1)
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
