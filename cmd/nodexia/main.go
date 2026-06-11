package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/app"
	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/logging"
)

// version is overridden at link time (-X main.version=...) and should match VERSION / release tags.
var version = "dev"

func main() {
	if len(os.Args) > 1 && strings.EqualFold(strings.TrimSpace(os.Args[1]), "healthcheck") {
		runHealthcheck()
		return
	}

	cfg, err := config.Load(version)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	logging.Setup(cfg.Log)
	slog.Info("starting nodexia",
		slog.String("version", cfg.Version),
		slog.String("environment", cfg.Environment),
		slog.String("http_addr", cfg.HTTP.Address),
	)

	application, err := app.New(cfg)
	if err != nil {
		log.Fatalf("create application: %v", err)
	}

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- application.Run()
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serverErr:
		if err != nil {
			log.Fatalf("run server: %v", err)
		}
	case <-ctx.Done():
		slog.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()

	if err := application.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("shutdown server: %v", err)
	}
}

func runHealthcheck() {
	url := strings.TrimSpace(os.Getenv("NODEXIA_HEALTHCHECK_URL"))
	if url == "" {
		url = "http://127.0.0.1:8080/healthz"
	}

	timeout := 3 * time.Second
	if rawTimeout := strings.TrimSpace(os.Getenv("NODEXIA_HEALTHCHECK_TIMEOUT")); rawTimeout != "" {
		parsed, err := time.ParseDuration(rawTimeout)
		if err != nil {
			log.Fatalf("healthcheck: parse timeout: %v", err)
		}
		timeout = parsed
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		log.Fatalf("healthcheck: request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Fatalf("healthcheck: unexpected status code %d", resp.StatusCode)
	}
}
