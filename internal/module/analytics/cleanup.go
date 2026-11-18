package analytics

import (
	"context"
	"log/slog"
	"time"
)

const (
	rawRetention    = 30 * 24 * time.Hour      // 30 days
	hourlyRetention = 6 * 30 * 24 * time.Hour  // ~6 months
	dailyRetention  = 2 * 365 * 24 * time.Hour // ~2 years
)

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
}
