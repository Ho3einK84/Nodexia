package monitoring

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
	return "monitoring"
}

func (Module) RouteGroup() string {
	return "/servers/{id}/monitoring"
}

func (Module) RegisterRoutes(mux *http.ServeMux, deps module.Dependencies) {
	if deps.Database == nil || deps.Database.SQL == nil || deps.SSH == nil {
		mux.Handle("GET /servers/{id}/monitoring", module.NewPlaceholderHandler(deps, module.PlaceholderPage{
			Title:      "Monitoring",
			RouteGroup: "/servers/{id}/monitoring",
			Description: "The monitoring page needs both the database and SSH runtime to collect and store resource snapshots.",
		}))
		return
	}

	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	snapshotRepo := NewSQLRepository(deps.Database.SQL)
	trafficRepo := NewSQLRepository(deps.Database.SQL)
	mux.Handle("GET /servers/{id}/monitoring", NewPageHandler(deps, serverRepo, snapshotRepo, trafficRepo))
	mux.Handle("POST /servers/{id}/monitoring", NewRefreshHandler(deps, serverRepo, snapshotRepo, trafficRepo))
}
