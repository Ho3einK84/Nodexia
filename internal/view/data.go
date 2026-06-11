package view

import "github.com/Ho3einK84/Nodexia/internal/config"

type NavItem struct {
	Label  string
	Href   string
	Active bool
}

func NewPageData(cfg config.Config) PageData {
	return PageData{
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
}

func NewErrorPageData(cfg config.Config, statusCode int, title, message string) PageData {
	data := NewPageData(cfg)
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
		{Label: "Overview", Href: "/"},
		{Label: "Servers", Href: "/servers"},
		{Label: "Diagnostics", Href: "/ops/diagnostics"},
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
