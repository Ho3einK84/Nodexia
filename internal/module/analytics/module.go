package analytics

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
	return "analytics"
}

func (Module) RouteGroup() string {
	return "/analytics"
}

func (Module) RegisterRoutes(mux *http.ServeMux, deps module.Dependencies) {
	if deps.Database == nil || deps.Database.SQL == nil {
		mux.Handle("GET /analytics", module.NewPlaceholderHandler(deps, module.PlaceholderPage{
			TitleKey:       "nav.analytics",
			RouteGroup:     "/analytics",
			DescriptionKey: "module.placeholder.analytics",
		}))
		return
	}

	writeRepo := NewSQLRepository(deps.Database.SQL)
	readDB := deps.Database.Reader()
	readRepo := NewSQLRepository(readDB)
	forecastSvc := NewForecastService()
	readServerRepo := servers.NewSQLRepository(readDB)

	limitsAdmin := NewLimitsAdminHandler(deps, writeRepo)

	mux.Handle("GET /analytics", NewGlobalHandler(deps, readRepo))
	mux.HandleFunc("GET /analytics/limits", limitsAdmin.Page)
	mux.HandleFunc("POST /analytics/limits", limitsAdmin.SetGlobal)
	mux.HandleFunc("POST /analytics/limits/tags", limitsAdmin.SetTag)
	mux.HandleFunc("POST /analytics/limits/tags/delete", limitsAdmin.DeleteTag)
	mux.Handle("GET /servers/{id}/analytics", NewPageHandler(deps, readServerRepo, readRepo))
	mux.Handle("GET /servers/{id}/analytics/data", NewDataHandler(deps, readServerRepo, readRepo))
	mux.Handle("GET /servers/{id}/analytics/forecast", NewForecastHandler(deps, readServerRepo, readRepo, forecastSvc))
	mux.Handle("POST /servers/{id}/analytics/limit", NewLimitHandler(deps, readServerRepo, writeRepo))
}
