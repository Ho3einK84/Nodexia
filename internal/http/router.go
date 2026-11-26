package webhttp

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/commandstream"
	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/db"
	"github.com/Ho3einK84/Nodexia/internal/http/handlers"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/i18n"
	"github.com/Ho3einK84/Nodexia/internal/livemetrics"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/registry"
	"github.com/Ho3einK84/Nodexia/internal/ratelimit"
	"github.com/Ho3einK84/Nodexia/internal/scheduler"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/terminalticket"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

func NewRouter(cfg config.Config, database *db.Runtime, sshService *sshclient.Service, commandStreams *commandstream.Store, terminalTickets *terminalticket.Store, liveMetrics *livemetrics.Hub, renderer *view.Renderer, staticFiles fs.FS, backgroundScheduler *scheduler.Runtime, modules []module.Module) http.Handler {
	mux := http.NewServeMux()
	notFoundHandler := handlers.NewErrorHandler(
		cfg,
		renderer,
		http.StatusNotFound,
		"Page not found",
		"The requested page does not exist or has not been implemented yet.",
	)
	internalErrorPreviewHandler := handlers.NewErrorHandler(
		cfg,
		renderer,
		http.StatusInternalServerError,
		"Internal server error",
		"Something went wrong while rendering the requested page.",
	)

	deps := module.Dependencies{
		Config:          cfg,
		Database:        database,
		SSH:             sshService,
		CommandStreams:  commandStreams,
		TerminalTickets: terminalTickets,
		LiveMetrics:     liveMetrics,
		Renderer:        renderer,
	}
	// Assign the scheduler as the country resolver only when it is actually
	// present, so the interface field stays a true nil (not a typed nil) when the
	// scheduler is unavailable and handlers can rely on a plain nil check.
	if backgroundScheduler != nil {
		deps.CountryResolver = backgroundScheduler
	}

	health := handlers.NewHealthHandler(cfg, database)

	loginThrottle := ratelimit.NewLoginThrottle(5, 30*time.Second, 15*time.Minute)
	loginHandler := handlers.NewLoginHandler(cfg, renderer, loginThrottle)
	mux.HandleFunc("GET /login", loginHandler.ServeHTTP)
	mux.HandleFunc("POST /login", loginHandler.ServeHTTP)
	mux.Handle("POST /logout", handlers.NewLogoutHandlerWithTickets(cfg, terminalTickets))

	mux.Handle("GET /{$}", handlers.NewHomeHandler(cfg, database, renderer, backgroundScheduler, registry.RouteGroups(modules)))
	diagHandler := handlers.NewDiagnosticsHandler(cfg, database, renderer, backgroundScheduler, commandStreams)
	mux.Handle("GET /ops/diagnostics", diagHandler)
	mux.HandleFunc("POST /ops/scheduler/{serverID}/{jobType}/toggle", diagHandler.SchedulerToggle)
	mux.HandleFunc("POST /ops/backup/export", diagHandler.BackupExport)
	mux.HandleFunc("POST /ops/backup/inspect", diagHandler.BackupInspect)
	mux.HandleFunc("POST /ops/backup/import", diagHandler.BackupImport)
	mux.Handle("GET /errors/not-found", notFoundHandler)
	mux.Handle("GET /errors/internal", internalErrorPreviewHandler)
	mux.Handle("GET /api/csrf-token", handlers.NewCSRFTokenHandler())
	mux.HandleFunc("GET /healthz", health.Liveness)
	mux.HandleFunc("GET /healthz/live", health.Live)
	mux.HandleFunc("GET /healthz/ready", health.Ready)

	// Prometheus-style metrics: token-gated in the handler and 404 when no
	// token is configured. Bypasses cookie auth (scrapers have no session).
	mux.Handle("GET /metrics", handlers.NewMetricsHandler(cfg, database, backgroundScheduler))

	// Language switcher: persists an explicit locale choice and redirects back.
	localeBundle := i18n.MustDefault()
	mux.Handle("GET /lang/{code}", handlers.NewLangHandler(localeBundle, cfg.Security.SessionCookieSecure))

	mux.Handle("GET /static/", staticAssetHandler(staticFiles))

	// Progressive Web App entry points. The manifest and service worker are
	// served from dedicated routes (see handlers/pwa.go) so they carry the right
	// scope and headers; both are public, like /static.
	mux.Handle("GET /manifest.webmanifest", handlers.NewManifestHandler(cfg))
	mux.Handle("GET /sw.js", handlers.NewServiceWorkerHandler(staticFiles))

	for _, mod := range modules {
		mod.RegisterRoutes(mux, deps)
	}

	return middleware.Chain(
		mux,
		middleware.RequestID(),
		middleware.SecurityHeaders(),
		middleware.Session(cfg),
		middleware.Locale(localeBundle),
		middleware.RequireAuth(cfg),
		middleware.CSRF(cfg),
		middleware.Logging(),
		middleware.Compress(),
		middleware.Recover(cfg, renderer),
	)
}

// staticAssetHandler serves the embedded /static tree with cache validators.
//
// In addition to `/static/<file>?v=<hash>`, it supports `/static/v/<digest>/<file>`
// so native ES module entry points and all of their relative `./modules/*.js`
// imports inherit immutable 1-year browser and Service Worker caching with zero
// cross-release version skew.
func staticAssetHandler(staticFiles fs.FS) http.Handler {
	etags := staticETags(staticFiles)
	fileServer := http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relPath := strings.TrimPrefix(r.URL.Path, "/static/")
		isImmutablePrefix := false
		if strings.HasPrefix(relPath, "v/") {
			rest := strings.TrimPrefix(relPath, "v/")
			if slash := strings.IndexByte(rest, '/'); slash > 0 && slash+1 < len(rest) {
				relPath = rest[slash+1:]
				isImmutablePrefix = true
				r2 := r.Clone(r.Context())
				r2.URL.Path = "/static/" + relPath
				r = r2
			}
		}

		if etag := etags[relPath]; etag != "" {
			w.Header().Set("ETag", etag)
		}
		if isImmutablePrefix || strings.HasPrefix(relPath, "fonts/") || r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		fileServer.ServeHTTP(w, r)
	})
}

// staticETags precomputes a strong, content-derived ETag for every embedded
// static file (keyed by its path relative to the static root, e.g. "style.css"
// or "fonts/exo2-latin.woff2"). Hashing once at startup keeps request handling
// allocation-free; the content is fixed for the life of the binary.
func staticETags(staticFiles fs.FS) map[string]string {
	etags := make(map[string]string)
	_ = fs.WalkDir(staticFiles, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		f, openErr := staticFiles.Open(path)
		if openErr != nil {
			return nil
		}
		defer func() {
			_ = f.Close()
		}()
		hash := sha256.New()
		if _, copyErr := io.Copy(hash, f); copyErr != nil {
			return nil
		}
		etags[path] = `"` + hex.EncodeToString(hash.Sum(nil)[:16]) + `"`
		return nil
	})
	return etags
}
