package alerts

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
	return "alerts"
}

func (Module) RouteGroup() string {
	return "/alerts"
}

func (Module) RegisterRoutes(mux *http.ServeMux, deps module.Dependencies) {
	if deps.Database == nil || deps.Database.SQL == nil {
		mux.Handle("GET /alerts", module.NewPlaceholderHandler(deps, module.PlaceholderPage{
			Title:       "Alerts",
			RouteGroup:  "/alerts",
			Description: "The database runtime is not available yet, so alert rules, channels, and silences cannot load.",
		}))
		return
	}

	repo := NewSQLRepository(deps.Database.SQL)
	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	h := NewHandlers(deps, repo, serverRepo)

	mux.HandleFunc("GET /alerts", h.Overview)

	mux.HandleFunc("GET /alerts/rules/new", h.RuleNew)
	mux.HandleFunc("POST /alerts/rules", h.RuleCreate)
	mux.HandleFunc("GET /alerts/rules/{id}/edit", h.RuleEdit)
	mux.HandleFunc("POST /alerts/rules/{id}/edit", h.RuleUpdate)
	mux.HandleFunc("POST /alerts/rules/{id}/delete", h.RuleDelete)

	mux.HandleFunc("GET /alerts/channels/new", h.ChannelNew)
	mux.HandleFunc("POST /alerts/channels", h.ChannelCreate)
	mux.HandleFunc("GET /alerts/channels/{id}/edit", h.ChannelEdit)
	mux.HandleFunc("POST /alerts/channels/{id}/edit", h.ChannelUpdate)
	mux.HandleFunc("POST /alerts/channels/{id}/delete", h.ChannelDelete)

	mux.HandleFunc("POST /alerts/silences", h.SilenceCreate)
	mux.HandleFunc("POST /alerts/silences/{id}/delete", h.SilenceDelete)

	// Server-scoped convenience: mute a metric for one server in one click.
	mux.HandleFunc("POST /servers/{id}/alerts/silence", h.ServerSilence)
}
