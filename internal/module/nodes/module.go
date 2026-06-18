package nodes

import (
	"net/http"

	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
)

type Module struct{}

func New() Module {
	return Module{}
}

func (Module) Name() string {
	return "nodes"
}

func (Module) RouteGroup() string {
	return "/servers/{id}/nodes"
}

func (Module) RegisterRoutes(mux *http.ServeMux, deps module.Dependencies) {
	if deps.Database == nil || deps.Database.SQL == nil || deps.SSH == nil || deps.CommandStreams == nil {
		mux.Handle("GET /servers/{id}/nodes", module.NewPlaceholderHandler(deps, module.PlaceholderPage{
			TitleKey:       "common.nodes",
			RouteGroup:     "/servers/{id}/nodes",
			DescriptionKey: "module.placeholder.nodes",
		}))
		return
	}

	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	repo := NewSQLRepository(deps.Database.SQL)
	handlers := NewHandlers(deps, serverRepo, repo, DefaultProviders())

	mux.HandleFunc("GET /servers/{id}/nodes", handlers.Page)
	mux.HandleFunc("POST /servers/{id}/nodes", handlers.Refresh)
	mux.HandleFunc("POST /servers/{id}/nodes/actions", handlers.Action)
	mux.HandleFunc("GET /servers/{id}/nodes/stream/{stream}/events", handlers.NodeStreamEvents)
	mux.HandleFunc("GET /servers/{id}/nodes/credentials", handlers.Credentials)
	mux.HandleFunc("POST /servers/{id}/nodes/install", handlers.InstallStart)
	mux.HandleFunc("POST /servers/{id}/nodes/install/rebecca", handlers.RebeccaInstallStart)
	mux.HandleFunc("GET /servers/{id}/nodes/install/{job}", handlers.InstallJob)
	mux.HandleFunc("GET /servers/{id}/nodes/install/{job}/events", handlers.InstallEvents)
}
