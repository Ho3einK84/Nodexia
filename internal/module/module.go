package module

import (
	"net/http"
	"strings"

	"github.com/Ho3einK84/Nodexia/internal/commandstream"
	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/db"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/livemetrics"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/terminalticket"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

type Dependencies struct {
	Config          config.Config
	Database        *db.Runtime
	SSH             *sshclient.Service
	CommandStreams  *commandstream.Store
	TerminalTickets *terminalticket.Store
	LiveMetrics     *livemetrics.Hub
	Renderer        *view.Renderer
	// CountryResolver triggers background country detection for a server (e.g.
	// after create/update). It is satisfied by the scheduler runtime and may be
	// nil when the scheduler is unavailable, so callers must nil-check it.
	CountryResolver CountryResolver
}

// CountryResolver kicks off best-effort, non-blocking country detection for a
// single server over its SSH connection. Implementations must never block the
// caller and must tolerate a missing/unreachable server.
type CountryResolver interface {
	ResolveCountryAsync(serverID int64)
}

type Module interface {
	Name() string
	RouteGroup() string
	RegisterRoutes(mux *http.ServeMux, deps Dependencies)
}

// PlaceholderPage describes the fallback page a module renders when its
// runtime dependencies are unavailable. TitleKey and DescriptionKey are i18n
// catalog keys (not literals): a module's definition is static and registered
// once at startup, but the page is rendered per request, so the keys are
// resolved against the request's active localizer in NewPlaceholderHandler —
// that handler closure is the per-request seam where a language is known.
// RouteGroup is a machine value (the URL prefix) and stays untranslated.
type PlaceholderPage struct {
	TitleKey       string
	RouteGroup     string
	DescriptionKey string
}

func NewPlaceholderHandler(deps Dependencies, page PlaceholderPage) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		viewModel := view.NewPageData(deps.Config, r)
		viewModel.CSRFToken = middleware.GetCSRFToken(r.Context())
		title := viewModel.T(page.TitleKey)
		description := viewModel.T(page.DescriptionKey)
		viewModel.Title = title
		viewModel.ContentTemplate = "content-module-placeholder"
		viewModel.PageTitle = title
		viewModel.PageDescription = description
		if strings.HasPrefix(page.RouteGroup, "/servers") {
			viewModel.ActiveNav = "/servers"
		}
		viewModel.ModuleName = title
		viewModel.ModuleRouteGroup = page.RouteGroup
		viewModel.ModuleDescription = description

		if err := deps.Renderer.Render(w, http.StatusOK, viewModel); err != nil {
			http.Error(w, "render page", http.StatusInternalServerError)
		}
	})
}
