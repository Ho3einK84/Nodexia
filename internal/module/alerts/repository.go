package alerts

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when an alert rule, channel, or silence does not exist.
var ErrNotFound = errors.New("alerts: not found")

// Rule binds a metric threshold to an optional server and channel. A nil
// ServerID means the rule is global (applies to every server); a nil ChannelID
// means notifications go to every enabled channel.
type Rule struct {
	ID              int64
	ServerID        *int64
	Metric          string
	Comparator      string
	Threshold       float64
	ConsecutiveHits int
	CooldownSeconds int
	Severity        string
	ChannelID       *int64
	Enabled         bool
	Note            string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// IsGlobal reports whether the rule applies to every server.
func (r Rule) IsGlobal() bool {
	return r.ServerID == nil
}

// Channel is a notification destination. The Telegram bot token is never stored
// here; only the non-secret chat id and optional message template are.
type Channel struct {
	ID              int64
	Kind            string
	Name            string
	ChatID          string
	MessageTemplate string
	Enabled         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Silence mutes a metric (or the "all" wildcard) for a single server until it
// is removed or, when ExpiresAt is set, until that time passes.
type Silence struct {
	ID        int64
	ServerID  int64
	Metric    string
	Reason    string
	ExpiresAt *time.Time
	CreatedAt time.Time
}

// IsActive reports whether the silence is in effect at the given moment.
func (s Silence) IsActive(now time.Time) bool {
	if s.ExpiresAt == nil {
		return true
	}
	return s.ExpiresAt.After(now)
}

// Repository persists alert rules, channels, and silences. SQL implementations
// must map a missing row to ErrNotFound and keep all statements portable across
// SQLite and MySQL.
type Repository interface {
	// Rules.
	CreateRule(ctx context.Context, rule Rule) (Rule, error)
	GetRule(ctx context.Context, id int64) (Rule, error)
	ListRules(ctx context.Context) ([]Rule, error)
	UpdateRule(ctx context.Context, rule Rule) (Rule, error)
	DeleteRule(ctx context.Context, id int64) error
	// ListEnabledRulesForServer returns enabled global rules plus enabled rules
	// scoped to the given server.
	ListEnabledRulesForServer(ctx context.Context, serverID int64) ([]Rule, error)

	// Channels.
	CreateChannel(ctx context.Context, channel Channel) (Channel, error)
	GetChannel(ctx context.Context, id int64) (Channel, error)
	ListChannels(ctx context.Context) ([]Channel, error)
	ListEnabledChannels(ctx context.Context) ([]Channel, error)
	UpdateChannel(ctx context.Context, channel Channel) (Channel, error)
	DeleteChannel(ctx context.Context, id int64) error

	// Silences.
	CreateSilence(ctx context.Context, silence Silence) (Silence, error)
	GetSilence(ctx context.Context, id int64) (Silence, error)
	ListSilences(ctx context.Context) ([]Silence, error)
	ListSilencesForServer(ctx context.Context, serverID int64) ([]Silence, error)
	DeleteSilence(ctx context.Context, id int64) error
	// IsSilenced reports whether the given metric is currently muted for the
	// server, honoring expiry and the "all" wildcard.
	IsSilenced(ctx context.Context, serverID int64, metric string) (bool, error)
}
