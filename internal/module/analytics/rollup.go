package analytics

import (
	"context"
	"log/slog"
	"time"
)

// RollupService computes hourly and daily metric aggregations from raw
// system_snapshots data. It is called by the scheduler on an hourly basis.
type RollupService struct {
	repo Repository
}

func NewRollupService(repo Repository) *RollupService {
	return &RollupService{repo: repo}
}

// ComputeHourlyRollups processes all completed hours that do not yet have an
// hourly rollup record, for every server that has raw data.
func (s *RollupService) ComputeHourlyRollups(ctx context.Context) {
	serverIDs, err := s.repo.ListServerIDs(ctx)
	if err != nil {
		slog.Warn("analytics: list server ids for hourly rollup", slog.String("error", err.Error()))
		return
	}

	// Process raw data from the past 30 days (raw retention window).
	since := time.Now().UTC().Add(-30 * 24 * time.Hour)

	for _, serverID := range serverIDs {
		if err := s.computeHourlyForServer(ctx, serverID, since); err != nil {
			slog.Warn("analytics: hourly rollup failed",
				slog.Int64("server_id", serverID),
				slog.String("error", err.Error()),
			)
		}
	}
}

// ComputeDailyRollups builds daily aggregations from hourly rollup data.
func (s *RollupService) ComputeDailyRollups(ctx context.Context) {
	serverIDs, err := s.repo.ListServerIDs(ctx)
	if err != nil {
		slog.Warn("analytics: list server ids for daily rollup", slog.String("error", err.Error()))
		return
	}

	since := time.Now().UTC().Add(-6 * 30 * 24 * time.Hour) // 6 months

	for _, serverID := range serverIDs {
		if err := s.computeDailyForServer(ctx, serverID, since); err != nil {
			slog.Warn("analytics: daily rollup failed",
				slog.Int64("server_id", serverID),
				slog.String("error", err.Error()),
			)
		}
	}
}

func (s *RollupService) computeHourlyForServer(ctx context.Context, serverID int64, since time.Time) error {
	points, err := s.repo.ListRawSince(ctx, serverID, since)
	if err != nil {
		return err
	}
	if len(points) == 0 {
		return nil
	}

	// Group points by truncated hour.
	buckets := make(map[time.Time][]RawPoint)
	for _, p := range points {
		hour := truncateToHour(p.RecordedAt)
		buckets[hour] = append(buckets[hour], p)
	}

	now := time.Now().UTC()
	currentHour := truncateToHour(now)

	for hour, pts := range buckets {
		// Never roll up the current (incomplete) hour.
		if !hour.Before(currentHour) {
			continue
		}
		exists, err := s.repo.HasHourlyRollup(ctx, serverID, hour)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		rollup := aggregateHourly(pts)
		rollup.PeriodStart = hour
		if err := s.repo.InsertHourlyRollup(ctx, serverID, rollup); err != nil {
			return err
		}
	}
	return nil
}

func (s *RollupService) computeDailyForServer(ctx context.Context, serverID int64, since time.Time) error {
	rollups, err := s.repo.ListHourlyRollups(ctx, serverID, since)
	if err != nil {
		return err
	}
	if len(rollups) == 0 {
		return nil
	}

	// Group hourly rollups by day.
	type hourlyGroup struct {
		rollups []HourlyRollup
	}
	buckets := make(map[time.Time]*hourlyGroup)
	for _, r := range rollups {
		day := truncateToDay(r.PeriodStart)
		if buckets[day] == nil {
			buckets[day] = &hourlyGroup{}
		}
		buckets[day].rollups = append(buckets[day].rollups, r)
	}

	now := time.Now().UTC()
	currentDay := truncateToDay(now)

	for day, group := range buckets {
		if !day.Before(currentDay) {
			continue
		}
		exists, err := s.repo.HasDailyRollup(ctx, serverID, day)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		rollup := aggregateDaily(group.rollups)
		rollup.PeriodStart = day
		if err := s.repo.InsertDailyRollup(ctx, serverID, rollup); err != nil {
			return err
		}
	}
	return nil
}

func aggregateHourly(points []RawPoint) HourlyRollup {
	if len(points) == 0 {
		return HourlyRollup{}
	}
	var sumCPU, sumRAM, sumDisk, sumSwap, sumL1, sumL5, sumL15 float64
	for _, p := range points {
		sumCPU += p.CPUUsage
		sumRAM += p.RAMUsage
		sumDisk += p.DiskUsage
		sumSwap += p.SwapUsage
		sumL1 += p.LoadAvg1
		sumL5 += p.LoadAvg5
		sumL15 += p.LoadAvg15
	}
	n := float64(len(points))
	return HourlyRollup{
		AvgCPU:      sumCPU / n,
		AvgRAM:      sumRAM / n,
		AvgDisk:     sumDisk / n,
		AvgSwap:     sumSwap / n,
		AvgLoad1:    sumL1 / n,
		AvgLoad5:    sumL5 / n,
		AvgLoad15:   sumL15 / n,
		SampleCount: len(points),
	}
}

func aggregateDaily(rollups []HourlyRollup) DailyRollup {
	if len(rollups) == 0 {
		return DailyRollup{}
	}
	var sumCPU, sumRAM, sumDisk, sumSwap, sumL1, sumL5, sumL15 float64
	totalSamples := 0
	for _, r := range rollups {
		sumCPU += r.AvgCPU
		sumRAM += r.AvgRAM
		sumDisk += r.AvgDisk
		sumSwap += r.AvgSwap
		sumL1 += r.AvgLoad1
		sumL5 += r.AvgLoad5
		sumL15 += r.AvgLoad15
		totalSamples += r.SampleCount
	}
	n := float64(len(rollups))
	return DailyRollup{
		AvgCPU:      sumCPU / n,
		AvgRAM:      sumRAM / n,
		AvgDisk:     sumDisk / n,
		AvgSwap:     sumSwap / n,
		AvgLoad1:    sumL1 / n,
		AvgLoad5:    sumL5 / n,
		AvgLoad15:   sumL15 / n,
		SampleCount: totalSamples,
	}
}

func truncateToHour(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, time.UTC)
}

func truncateToDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
