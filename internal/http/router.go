package webhttp

import (
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/commandstream"
	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/db"
	"github.com/Ho3einK84/Nodexia/internal/http/handlers"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
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
	mux.Handle("GET /logout", handlers.NewLogoutHandler(cfg.Security.SessionCookieSecure))

	mux.Handle("GET /{$}", handlers.NewHomeHandler(cfg, database, renderer, backgroundScheduler, registry.RouteGroups(modules)))
	diagHandler := handlers.NewDiagnosticsHandler(cfg, database, renderer, backgroundScheduler, commandStreams)
	mux.Handle("GET /ops/diagnostics", diagHandler)
	mux.HandleFunc("POST /ops/scheduler/{serverID}/{jobType}/toggle", diagHandler.SchedulerToggle)
	mux.Handle("GET /errors/not-found", notFoundHandler)
	mux.Handle("GET /errors/internal", internalErrorPreviewHandler)
	mux.HandleFunc("GET /healthz", health.Liveness)
	mux.HandleFunc("GET /healthz/live", health.Live)
	mux.HandleFunc("GET /healthz/ready", health.Ready)

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
		middleware.CSRF(cfg),
		middleware.RequireAuth(cfg),
		middleware.Logging(),
		middleware.Recover(cfg, renderer),
	)
}

// staticAssetHandler serves the embedded /static tree. Font files are
// immutable — their content never changes under a given name — so they are
// served with a one-year immutable cache directive to avoid revalidation on
// every navigation. Other assets (CSS/JS) are left to the service worker's
// stale-while-revalidate strategy and normal validators.
func staticAssetHandler(staticFiles fs.FS) http.Handler {
	fileServer := http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/fonts/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fileServer.ServeHTTP(w, r)
	})
}
