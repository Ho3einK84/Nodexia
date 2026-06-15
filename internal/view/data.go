package view

import (
	"net/http"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/i18n"
)

type NavItem struct {
	// Label is the English name; it doubles as the stable identifier the
	// templates switch on to pick an icon, so it stays English regardless of
	// locale. Key is the translation key used to render the visible text.
	Label  string
	Key    string
	Href   string
	Active bool
}

// NewPageData builds the base page data for a request. It attaches the active
// localizer resolved by the locale middleware (falling back to the default
// language when absent) so every page renders in the user's language and with
// the correct text direction.
func NewPageData(cfg config.Config, r *http.Request) PageData {
	data := PageData{
		AppName:         cfg.App.Name,
		Environment:     cfg.Environment,
		Version:         cfg.Version,
		HTTPAddress:     cfg.HTTP.Address,
		DatabaseDriver:  cfg.Database.Driver,
		DatabaseTarget:  DatabaseTarget(cfg),
		EnvFile:         cfg.Install.EnvFile,
		Description:     "Self-hosted control panel for monitoring and managing Rebecca and PasarGuard panel nodes.",
		FooterNote:      "Open-source, self-hosted monitoring and node management for Rebecca and PasarGuard.",
		NavigationItems: defaultNavigation(""),
	}
	data.SetLocalizer(localizerFor(r))
	return data
}

// localizerFor returns the request's active localizer, falling back to the
// default-language localizer when the request carries none (e.g. it bypassed
// the locale middleware).
func localizerFor(r *http.Request) *i18n.Localizer {
	if r != nil {
		if loc := i18n.FromContext(r.Context()); loc != nil {
			return loc
		}
	}
	return i18n.MustDefault().Localizer(i18n.DefaultLanguage)
}

func NewErrorPageData(cfg config.Config, r *http.Request, statusCode int, title, message string) PageData {
	data := NewPageData(cfg, r)
	data.Title = title
	data.Description = message
	data.ContentTemplate = "content-error"
	data.PageTitle = title
	data.PageDescription = message
	data.StatusCode = statusCode
	data.ErrorTitle = title
	data.ErrorMessage = message
	return data
}

func DatabaseTarget(cfg config.Config) string {
	if cfg.Database.Driver == config.DriverMySQL {
		return "custom mysql dsn"
	}

	return cfg.Database.SQLitePath
}

func defaultNavigation(activeHref string) []NavItem {
	items := []NavItem{
		{Label: "Overview", Key: "nav.overview", Href: "/"},
		{Label: "Servers", Key: "nav.servers", Href: "/servers"},
		{Label: "Analytics", Key: "nav.analytics", Href: "/analytics"},
		{Label: "Alerts", Key: "nav.alerts", Href: "/alerts"},
		{Label: "Diagnostics", Key: "nav.diagnostics", Href: "/ops/diagnostics"},
	}

	for index := range items {
		items[index].Active = items[index].Href == activeHref
	}

	return items
}

type DiagnosticsView struct {
	StartedAt          string
	Uptime             string
	GoVersion          string
	NumCPU             int
	Goroutines         int
	DatabaseStatus     string
	DatabaseDetail     string
	MigrationCount     int
	CommandStreamCount int
	HealthLiveURL      string
	HealthReadyURL     string
	SSHHostKeyPolicy   string
	SchedulerEnabled   bool
	BehindReverseProxy bool
}
