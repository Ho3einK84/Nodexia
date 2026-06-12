package terminal

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
	return "terminal"
}

func (Module) RouteGroup() string {
	return "/servers/{id}/terminal"
}

func (Module) RegisterRoutes(mux *http.ServeMux, deps module.Dependencies) {
	if deps.Database == nil || deps.Database.SQL == nil ||
		deps.SSH == nil || deps.TerminalTickets == nil {
		mux.Handle("GET /servers/{id}/terminal", module.NewPlaceholderHandler(deps, module.PlaceholderPage{
			Title:       "Terminal",
			RouteGroup:  "/servers/{id}/terminal",
			Description: "The interactive terminal requires the database, SSH runtime, and ticket store.",
		}))
		return
	}

	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	pageHandler := newPageHandler(deps, serverRepo)
	wsHandler := newWSHandler(deps, serverRepo)

	mux.Handle("GET /servers/{id}/terminal", pageHandler)
	mux.Handle("POST /servers/{id}/terminal", newPostHandler(deps, serverRepo))
	mux.Handle("GET /servers/{id}/terminal/ws", wsHandler)
}
