package servers

import (
	"context"
	"time"
)

type Server struct {
	ID                 int64
	Name               string
	Host               string
	Port               int
	AuthMode           string
	Username           string
	Note               string
	Tags               []string
	CredentialStrategy string
	CredentialRef      string
	// CountryCode is the detected ISO 3166-1 alpha-2 country code for the node's
	// public-IP egress (resolved over SSH, see internal/geoip), or "" when it is
	// unknown / undetectable. CountryName is the matching human-readable name.
	// CountryCheckedAt is the last resolution attempt (success or empty); a zero
	// value means detection has never run for this server.
	CountryCode      string
	CountryName      string
	CountryCheckedAt time.Time
	// TrafficResetDay anchors the server's traffic accounting period to the day
	// of the month the provider resets its quota. 1 (default) = calendar month;
	// 2–28 = that day of the month. Values outside 1–28 are rejected on input.
	TrafficResetDay int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Repository interface {
	Create(ctx context.Context, server Server) (Server, error)
	GetByID(ctx context.Context, id int64) (Server, error)
	List(ctx context.Context) ([]Server, error)
	Update(ctx context.Context, server Server) (Server, error)
	Delete(ctx context.Context, id int64) error
	// UpdateCountry persists a freshly detected country for one server and stamps
	// the check time. It is intentionally separate from Update so that country
	// detection (driven by the scheduler / async resolver) never collides with
	// form-driven edits and never touches credentials or other fields. An empty
	// code/name records "checked, nothing detected" so callers can back off.
	UpdateCountry(ctx context.Context, id int64, code, name string) error
}
