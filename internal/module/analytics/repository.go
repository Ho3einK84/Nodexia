package analytics

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("analytics: not found")

// RawPoint is one system_snapshots row projected for analytics queries.
type RawPoint struct {
	RecordedAt time.Time
	CPUUsage   float64
	RAMUsage   float64
	SwapUsage  float64
	DiskUsage  float64
	LoadAvg1   float64
	LoadAvg5   float64
	LoadAvg15  float64
}

// HourlyRollup is an aggregated record for one server/hour period.
type HourlyRollup struct {
	ID          int64
	ServerID    int64
	PeriodStart time.Time
	AvgCPU      float64
	AvgRAM      float64
	AvgDisk     float64
	AvgSwap     float64
	AvgLoad1    float64
	AvgLoad5    float64
	AvgLoad15   float64
	SampleCount int
}

// DailyRollup is an aggregated record for one server/day period.
type DailyRollup struct {
	ID          int64
	ServerID    int64
	PeriodStart time.Time
	AvgCPU      float64
	AvgRAM      float64
	AvgDisk     float64
	AvgSwap     float64
	AvgLoad1    float64
	AvgLoad5    float64
	AvgLoad15   float64
	SampleCount int
}

// TrafficDay holds one day of bandwidth totals from vnstat.
type TrafficDay struct {
	Label string
	RX    int64
	TX    int64
	Total int64
}

// TrafficMonth holds one month of bandwidth totals from vnstat.
type TrafficMonth struct {
	Label string
	RX    int64
	TX    int64
	Total int64
}

// ServerMetricSummary holds the latest resource metrics for one server.
type ServerMetricSummary struct {
	ServerID   int64
	ServerName string
	// CountryCode is the server's detected ISO 3166-1 alpha-2 code (or "" when
	// unknown), folded into the metric query so the overview can show a flag
	// without an extra per-row lookup.
	CountryCode string
	AvgCPU      float64
	AvgRAM      float64
	AvgDisk     float64
	AvgSwap     float64
}

// ServerTrafficSummary holds the current-month traffic totals for one server,
// split into download (RX) and upload (TX) plus the combined total.
type ServerTrafficSummary struct {
	ServerID   int64
	ServerName string
	// CountryCode is the server's detected ISO 3166-1 alpha-2 code (or "" when
	// unknown), folded into the traffic query so the overview can show a flag
	// without an extra per-row lookup.
	CountryCode string
	MonthRX     int64
	MonthTX     int64
	MonthBytes  int64
	MonthLabel  string
}

// Limit scopes for traffic_limit_rules. server-level caps live in their own
// server_traffic_limits table; these two cover the broader fallbacks.
const (
	LimitScopeGlobal = "global"
	LimitScopeTag    = "tag"
)

// ScopedLimit is one global/tag monthly download (RX) cap rule.
type ScopedLimit struct {
	Scope      string // LimitScopeGlobal or LimitScopeTag
	Ref        string // "" for global, the tag name for a tag rule
	LimitBytes int64
}

// Repository abstracts all analytics data access.
type Repository interface {
	ListRawSince(ctx context.Context, serverID int64, since time.Time) ([]RawPoint, error)
	ListHourlyRollups(ctx context.Context, serverID int64, since time.Time) ([]HourlyRollup, error)
	ListDailyRollups(ctx context.Context, serverID int64, since time.Time) ([]DailyRollup, error)
	HasHourlyRollup(ctx context.Context, serverID int64, periodStart time.Time) (bool, error)
	HasDailyRollup(ctx context.Context, serverID int64, periodStart time.Time) (bool, error)
	InsertHourlyRollup(ctx context.Context, serverID int64, r HourlyRollup) error
	InsertDailyRollup(ctx context.Context, serverID int64, r DailyRollup) error
	ListServerIDs(ctx context.Context) ([]int64, error)
	GetLatestTrafficForServer(ctx context.Context, serverID int64) ([]TrafficDay, []TrafficMonth, error)
	// GetTrafficLimit returns the configured monthly download (RX) limit in bytes
	// for a server. ok is false when no limit is configured (the common case),
	// which callers must treat as "unlimited" — never as a zero limit.
	GetTrafficLimit(ctx context.Context, serverID int64) (limitBytes int64, ok bool, err error)
	// SetTrafficLimit upserts a server's monthly download (RX) limit. The caller
	// is responsible for rejecting non-positive values before calling.
	SetTrafficLimit(ctx context.Context, serverID, limitBytes int64) error
	// DeleteTrafficLimit clears a server's limit (back to "unlimited"). Removing a
	// non-existent limit is a no-op, not an error.
	DeleteTrafficLimit(ctx context.Context, serverID int64) error
	// ResolveEffectiveLimit returns the monthly download (RX) cap that actually
	// applies to a server, honouring precedence: the server's own per-server limit
	// wins; otherwise the SMALLEST tag cap among the server's tags; otherwise the
	// fleet-wide global default. ok is false (unlimited) when none is configured.
	// source identifies where the limit came from: "server", "tag:<name>", or
	// "global" — for display only.
	ResolveEffectiveLimit(ctx context.Context, serverID int64, tags []string) (limitBytes int64, source string, ok bool, err error)
	// ListScopedLimits returns every global/tag limit rule, for the admin page.
	ListScopedLimits(ctx context.Context) ([]ScopedLimit, error)
	// GetScopedLimit reads one scope/ref limit rule (ok=false when absent).
	GetScopedLimit(ctx context.Context, scope, ref string) (limitBytes int64, ok bool, err error)
	// SetScopedLimit upserts a global/tag limit rule. Callers reject non-positive
	// values before calling, exactly like SetTrafficLimit.
	SetScopedLimit(ctx context.Context, scope, ref string, limitBytes int64) error
	// DeleteScopedLimit clears a global/tag limit rule (a missing row is a no-op).
	DeleteScopedLimit(ctx context.Context, scope, ref string) error
	DeleteRawBefore(ctx context.Context, before time.Time) (int64, error)
	DeleteHourlyBefore(ctx context.Context, before time.Time) (int64, error)
	DeleteDailyBefore(ctx context.Context, before time.Time) (int64, error)
	ListServerMetricSummaries(ctx context.Context, limit int) ([]ServerMetricSummary, error)
	ListServerTrafficSummaries(ctx context.Context, limit int) ([]ServerTrafficSummary, error)
}
