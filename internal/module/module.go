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

type PlaceholderPage struct {
	Title       string
	RouteGroup  string
	Description string
}

func NewPlaceholderHandler(deps Dependencies, page PlaceholderPage) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		viewModel := view.NewPageData(deps.Config, r)
		viewModel.CSRFToken = middleware.GetCSRFToken(r.Context())
		viewModel.Title = page.Title
		viewModel.ContentTemplate = "content-module-placeholder"
		viewModel.PageTitle = page.Title
		viewModel.PageDescription = page.Description
		if strings.HasPrefix(page.RouteGroup, "/servers") {
			viewModel.ActiveNav = "/servers"
		}
		viewModel.ModuleName = page.Title
		viewModel.ModuleRouteGroup = page.RouteGroup
		viewModel.ModuleDescription = page.Description

		if err := deps.Renderer.Render(w, http.StatusOK, viewModel); err != nil {
			http.Error(w, "render page", http.StatusInternalServerError)
		}
	})
}
