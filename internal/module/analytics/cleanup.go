package analytics

import (
	"context"
	"log/slog"
	"time"
)

const (
	rawRetention            = 30 * 24 * time.Hour      // 30 days
	hourlyRetention         = 6 * 30 * 24 * time.Hour  // ~6 months
	dailyRetention          = 2 * 365 * 24 * time.Hour // ~2 years
	resolvedAlertRetention  = 90 * 24 * time.Hour      // 90 days for resolved alert events
	commandHistoryPerServer = 500                      // newest 500 commands per server
)

type extendedCleanupRepo interface {
	PruneSupersededTraffic(ctx context.Context) (int64, error)
	DeleteResolvedAlertsBefore(ctx context.Context, before time.Time) (int64, error)
	TrimCommandHistoryPerServer(ctx context.Context, keepPerServer int) (int64, error)
}

// CleanupService purges expired metric data according to retention policies.
type CleanupService struct {
	repo Repository
}

func NewCleanupService(repo Repository) *CleanupService {
	return &CleanupService{repo: repo}
}

// RunCleanup deletes data older than the configured retention periods.
func (s *CleanupService) RunCleanup(ctx context.Context) {
	now := time.Now().UTC()

	if n, err := s.repo.DeleteRawBefore(ctx, now.Add(-rawRetention)); err != nil {
		slog.Warn("analytics: cleanup raw snapshots", slog.String("error", err.Error()))
	} else if n > 0 {
		slog.Info("analytics: cleaned up raw snapshots", slog.Int64("deleted", n))
	}

	if n, err := s.repo.DeleteHourlyBefore(ctx, now.Add(-hourlyRetention)); err != nil {
		slog.Warn("analytics: cleanup hourly rollups", slog.String("error", err.Error()))
	} else if n > 0 {
		slog.Info("analytics: cleaned up hourly rollups", slog.Int64("deleted", n))
	}

	if n, err := s.repo.DeleteDailyBefore(ctx, now.Add(-dailyRetention)); err != nil {
		slog.Warn("analytics: cleanup daily rollups", slog.String("error", err.Error()))
	} else if n > 0 {
		slog.Info("analytics: cleaned up daily rollups", slog.Int64("deleted", n))
	}

	if ext, ok := s.repo.(extendedCleanupRepo); ok {
		if n, err := ext.PruneSupersededTraffic(ctx); err != nil {
			slog.Warn("analytics: cleanup superseded vnstat snapshots", slog.String("error", err.Error()))
		} else if n > 0 {
			slog.Info("analytics: cleaned up superseded vnstat snapshots", slog.Int64("deleted", n))
		}
		if n, err := ext.DeleteResolvedAlertsBefore(ctx, now.Add(-resolvedAlertRetention)); err != nil {
			slog.Warn("analytics: cleanup resolved alert events", slog.String("error", err.Error()))
		} else if n > 0 {
			slog.Info("analytics: cleaned up resolved alert events", slog.Int64("deleted", n))
		}
		if n, err := ext.TrimCommandHistoryPerServer(ctx, commandHistoryPerServer); err != nil {
			slog.Warn("analytics: cleanup command history", slog.String("error", err.Error()))
		} else if n > 0 {
			slog.Info("analytics: cleaned up command history", slog.Int64("deleted", n))
		}
	}
}
