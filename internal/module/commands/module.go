package commands

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
	return "commands"
}

func (Module) RouteGroup() string {
	return "/servers/{id}/commands"
}

func (Module) RegisterRoutes(mux *http.ServeMux, deps module.Dependencies) {
	if deps.Database == nil || deps.Database.SQL == nil || deps.SSH == nil || deps.CommandStreams == nil {
		mux.Handle("GET /servers/{id}/commands", module.NewPlaceholderHandler(deps, module.PlaceholderPage{
			Title:      "Commands",
			RouteGroup: "/servers/{id}/commands",
			Description: "The command runner needs the database, SSH runtime, and live stream store before it can execute remote actions.",
		}))
		return
	}

	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	historyRepo := NewSQLRepository(deps.Database.SQL)
	mux.Handle("GET /servers/{id}/commands", NewPageHandler(deps, serverRepo, historyRepo))
	mux.Handle("POST /servers/{id}/commands", NewActionHandler(deps, serverRepo, historyRepo))
	mux.Handle("GET /servers/{id}/commands/stream/{stream}/events", NewStreamEventsHandler(deps))
}
