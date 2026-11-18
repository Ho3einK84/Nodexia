package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/db"
	"github.com/Ho3einK84/Nodexia/internal/geoip"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
)

const (
	// countryInitialDelay is how long after Start the first country sweep runs.
	// Short enough that a fresh install populates flags quickly, long enough to
	// stay out of the way of the startup burst.
	countryInitialDelay = 30 * time.Second
	// countrySweepInterval is how often the country sweep wakes up. Each wake only
	// resolves servers that are missing or stale, so this is cheap; it mainly
	// bounds how soon a newly added server is picked up if the create-time async
	// kick did not run (e.g. scheduler started after the server already existed).
	countrySweepInterval = time.Hour
	// countryRefreshInterval is the minimum age before an already-checked server is
	// re-resolved. Kept generous (daily) because the free geo services rate-limit
	// and a node's country rarely changes.
	countryRefreshInterval = 24 * time.Hour
	// countryResolveTimeout bounds a single resolution (connect + the multi-endpoint
	// probe). The remote probe tries up to three endpoints at ~6s each.
	countryResolveTimeout = 45 * time.Second
	// countryCommandTimeout bounds just the remote probe command. It must exceed
	// the worst-case sum of the per-endpoint curl/wget timeouts in geoip.Command.
	countryCommandTimeout = 30 * time.Second
)

// ResolveCountryAsync detects and stores one server's country in the background.
// It is the hook the server create/update handlers call so a flag appears
// without waiting on (or failing because of) a slow SSH round-trip. It never
// blocks the caller and never returns an error: detection failures are logged at
// debug and simply leave the country to be filled in by a later sweep.
//
// A nil receiver (scheduler unavailable) is a safe no-op.
func (r *Runtime) ResolveCountryAsync(serverID int64) {
	if r == nil {
		return
	}

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()

		ctx, cancel := context.WithTimeout(context.Background(), countryResolveTimeout)
		defer cancel()

		server, err := r.serverRepo.GetByID(ctx, serverID)
		if err != nil {
			return
		}
		if !r.evaluateEligibility(server).Allowed {
			// No usable credentials — nothing to connect with, so skip silently.
			return
		}
		if err := r.resolveCountry(ctx, server); err != nil {
			slog.Debug("country detection failed",
				slog.Int64("server_id", serverID),
				slog.String("error", err.Error()),
			)
		}
	}()
}

// countryLoop periodically refreshes server countries. It runs an initial sweep
// shortly after startup and then on a generous ticker; each sweep only touches
// servers that are missing a country or whose last check is stale.
func (r *Runtime) countryLoop(ctx context.Context) {
	defer r.wg.Done()

	initial := time.NewTimer(countryInitialDelay)
	defer initial.Stop()
	ticker := time.NewTicker(countrySweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-initial.C:
			r.refreshCountries(ctx)
		case <-ticker.C:
			r.refreshCountries(ctx)
		}
	}
}

// refreshCountries resolves the country for every eligible server that has never
// been checked or whose last check has aged past countryRefreshInterval. Servers
// are processed one at a time so the sweep never opens a burst of SSH
// connections, and a per-server timeout keeps one slow node from stalling the
// rest.
func (r *Runtime) refreshCountries(ctx context.Context) {
	serversList, err := r.serverRepo.List(ctx)
	if err != nil {
		return
	}

	now := time.Now().UTC()
	for _, server := range serversList {
		if ctx.Err() != nil {
			return
		}
		if !r.countryDue(server, now) {
			continue
		}
		if !r.evaluateEligibility(server).Allowed {
			continue
		}

		resolveCtx, cancel := context.WithTimeout(ctx, countryResolveTimeout)
		if err := r.resolveCountry(resolveCtx, server); err != nil {
			slog.Debug("country detection failed",
				slog.Int64("server_id", server.ID),
				slog.String("error", err.Error()),
			)
		}
		cancel()
	}
}

// countryDue reports whether a server should be (re)resolved now. It keys off the
// check timestamp rather than whether a code is present, so a server that
// genuinely has no detectable country (empty result stored with a fresh
// timestamp) backs off until the refresh interval instead of being retried every
// sweep.
func (r *Runtime) countryDue(server servers.Server, now time.Time) bool {
	if server.CountryCheckedAt.IsZero() {
		return true
	}
	return now.Sub(server.CountryCheckedAt) >= countryRefreshInterval
}

// resolveCountry runs the remote geo probe over SSH and persists the result.
//
// Failure handling is deliberate:
//   - A connection/SSH error returns the error WITHOUT persisting, so the check
//     timestamp stays put and the server is retried on the next sweep.
//   - A successful probe that yields no recognised code (no public egress,
//     blocked endpoints, garbage, or a private/reserved address — see
//     geoip.ParseResponse) stores an empty country WITH a fresh timestamp, which
//     records "checked, nothing to show" and backs detection off until the next
//     refresh interval instead of hammering the rate-limited services.
func (r *Runtime) resolveCountry(ctx context.Context, server servers.Server) error {
	req, _, err := r.connectionRequestFor(server)
	if err != nil {
		return err
	}
	req.Command = geoip.Command
	req.CommandTimeout = countryCommandTimeout

	result, err := r.ssh.RunCommand(ctx, req)
	if err != nil {
		return err
	}

	code, ok := geoip.ParseResponse(result.Stdout)
	name := ""
	if ok {
		name = geoip.CountryName(code)
	} else {
		code = ""
	}

	return db.RetryOnBusy(ctx, func() error {
		return r.serverRepo.UpdateCountry(ctx, server.ID, code, name)
	})
}
