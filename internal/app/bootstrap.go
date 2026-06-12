package app

import (
	"context"
	"io/fs"
	"time"

	assets "github.com/Ho3einK84/Nodexia"
	"github.com/Ho3einK84/Nodexia/internal/commandstream"
	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/db"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/registry"
	"github.com/Ho3einK84/Nodexia/internal/scheduler"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/terminalticket"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

type Bootstrap struct {
	Config          config.Config
	Database        *db.Runtime
	SSH             *sshclient.Service
	CommandStreams   *commandstream.Store
	TerminalTickets *terminalticket.Store
	Renderer        *view.Renderer
	StaticFiles     fs.FS
	Scheduler       *scheduler.Runtime
	Modules         []module.Module
}

func NewBootstrap(cfg config.Config) (Bootstrap, error) {
	dbRuntime, err := db.Open(context.Background(), cfg.Database)
	if err != nil {
		return Bootstrap{}, err
	}

	renderer, err := view.NewRenderer()
	if err != nil {
		_ = dbRuntime.Close()
		return Bootstrap{}, err
	}

	staticFiles, err := assets.Static()
	if err != nil {
		_ = dbRuntime.Close()
		return Bootstrap{}, err
	}

	sshService := sshclient.New(cfg.SSH, cfg.Security)
	commandStreams := commandstream.New(45 * time.Minute)
	terminalTickets := terminalticket.New(terminalticket.DefaultTTL)
	backgroundScheduler := scheduler.New(cfg, dbRuntime.SQL, sshService)
	if backgroundScheduler != nil {
		backgroundScheduler.Start()
	}

	return Bootstrap{
		Config:          cfg,
		Database:        dbRuntime,
		SSH:             sshService,
		CommandStreams:   commandStreams,
		TerminalTickets: terminalTickets,
		Renderer:        renderer,
		StaticFiles:     staticFiles,
		Scheduler:       backgroundScheduler,
		Modules:         registry.DefaultModules(),
	}, nil
}
