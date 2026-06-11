package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Ho3einK84/Nodexia/internal/config"
	apphttp "github.com/Ho3einK84/Nodexia/internal/http"
)

type App struct {
	server   *http.Server
	closers  []func() error
}

func New(cfg config.Config) (*App, error) {
	bootstrap, err := NewBootstrap(cfg)
	if err != nil {
		return nil, err
	}

	handler := apphttp.NewRouter(bootstrap.Config, bootstrap.Database, bootstrap.SSH, bootstrap.CommandStreams, bootstrap.Renderer, bootstrap.StaticFiles, bootstrap.Scheduler, bootstrap.Modules)

	server := &http.Server{
		Addr:         bootstrap.Config.HTTP.Address,
		Handler:      handler,
		ReadTimeout:  bootstrap.Config.HTTP.ReadTimeout,
		WriteTimeout: bootstrap.Config.HTTP.WriteTimeout,
		IdleTimeout:  bootstrap.Config.HTTP.IdleTimeout,
	}

	return &App{
		server: server,
		closers: []func() error{
			bootstrap.Scheduler.Close,
			bootstrap.Database.Close,
		},
	}, nil
}

func (a *App) Run() error {
	err := a.server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

func (a *App) Shutdown(ctx context.Context) error {
	shutdownErr := a.server.Shutdown(ctx)
	closeErr := closeAll(a.closers)

	if shutdownErr != nil && closeErr != nil {
		return errors.Join(shutdownErr, closeErr)
	}

	if shutdownErr != nil {
		return shutdownErr
	}

	return closeErr
}

func closeAll(closers []func() error) error {
	var combined error
	for _, closer := range closers {
		if closer == nil {
			continue
		}

		if err := closer(); err != nil {
			combined = errors.Join(combined, fmt.Errorf("close resource: %w", err))
		}
	}

	return combined
}
