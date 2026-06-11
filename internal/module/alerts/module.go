package alerts

import (
	"net/http"
	"strings"

	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/notify"
	"github.com/Ho3einK84/Nodexia/internal/notify/telegram"
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
	h := NewHandlers(deps, repo, serverRepo, buildNotifier(deps))

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
	mux.HandleFunc("POST /alerts/channels/{id}/test", h.ChannelTest)

	mux.HandleFunc("POST /alerts/silences", h.SilenceCreate)
	mux.HandleFunc("POST /alerts/silences/{id}/delete", h.SilenceDelete)

	// Server-scoped convenience: mute a metric for one server in one click.
	mux.HandleFunc("POST /servers/{id}/alerts/silence", h.ServerSilence)
}

// buildNotifier returns a Telegram notifier when a bot token is configured, or
// nil when it is not (the UI then shows a "not configured" notice). A typed-nil
// must never be returned as a non-nil interface, so this only constructs the
// client when the token is present.
func buildNotifier(deps module.Dependencies) notify.Notifier {
	token := strings.TrimSpace(deps.Config.Notify.TelegramBotToken)
	if token == "" {
		return nil
	}
	return telegram.NewClient(token)
}
