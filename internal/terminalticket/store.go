// Package terminalticket provides a short-lived, single-use ticket store for
// the in-browser SSH terminal.  Each ticket wraps a ConnectionRequest so that
// the WebSocket upgrade handler can start a shell without the user re-entering
// credentials.
//
// Lifecycle:
//   - Ticket is created by the POST /servers/{id}/terminal handler after
//     validating credentials.
//   - It expires ~30 s after creation if not consumed.
//   - Consuming a ticket is atomic and single-use: the WebSocket handler calls
//     Consume, which marks the ticket as used and returns the stored request.
//     Any subsequent call with the same id returns (_, false).
//   - Release removes the ticket after the session ends (for clean memory).
package terminalticket

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/sshclient"
)

const (
	DefaultTTL = 30 * time.Second
)

// Ticket is an opaque handle that carries the connection request for one
// terminal session.  All fields are read-only after creation.
type Ticket struct {
	ID        string
	ServerID  int64
	Req       sshclient.ConnectionRequest
	CreatedAt time.Time
}

// SessionCleaner is implemented by the terminal session hub to tear down
// active or detached PTY sessions on logout, server deletion, or shutdown.
type SessionCleaner interface {
	Close() error
	CloseForServer(serverID int64)
	CloseForUser(username string)
}

// Store is a concurrent-safe in-memory ticket store.  It also tracks the
// number of active terminal sessions per authenticated user so that the
// WebSocket handler can enforce a per-user concurrency limit.
type Store struct {
	ttl     time.Duration
	mu      sync.Mutex
	tickets map[string]*ticketState

	// sessionCounts maps username → current live terminal count.
	sessionMu     sync.Mutex
	sessionCounts map[string]int32

	cleanersMu sync.Mutex
	cleaners   []SessionCleaner
}

type ticketState struct {
	ticket Ticket
	used   atomic.Bool
}

// New creates a Store with the given ticket TTL.
func New(ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Store{
		ttl:           ttl,
		tickets:       make(map[string]*ticketState),
		sessionCounts: make(map[string]int32),
	}
}

// Create stores a new ticket and returns its ID.
func (s *Store) Create(serverID int64, req sshclient.ConnectionRequest) (string, error) {
	s.pruneExpired()

	id := newID()
	if id == "" {
		return "", errors.New("terminalticket: failed to generate ticket ID")
	}
	s.mu.Lock()
	s.tickets[id] = &ticketState{
		ticket: Ticket{
			ID:        id,
			ServerID:  serverID,
			Req:       req,
			CreatedAt: time.Now(),
		},
	}
	s.mu.Unlock()
	return id, nil
}

// Consume atomically marks the ticket as used and returns it.  Returns
// (Ticket, true) exactly once; subsequent calls with the same id return
// (Ticket{}, false).  Also returns false for unknown or expired ids.
func (s *Store) Consume(id string) (Ticket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, ok := s.tickets[id]
	if !ok {
		return Ticket{}, false
	}

	if time.Since(st.ticket.CreatedAt) > s.ttl {
		delete(s.tickets, id)
		return Ticket{}, false
	}

	if !st.used.CompareAndSwap(false, true) {
		return Ticket{}, false
	}
	return st.ticket, true
}

// Release removes a consumed ticket from the store.
func (s *Store) Release(id string) {
	s.mu.Lock()
	delete(s.tickets, id)
	s.mu.Unlock()
}

// TryAcquireSession increments the active-session counter for the user and
// returns true if it stays at or below max.  Returns false if the limit would
// be exceeded; in that case the counter is NOT incremented.
func (s *Store) TryAcquireSession(username string, max int) bool {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if int(s.sessionCounts[username]) >= max {
		return false
	}
	s.sessionCounts[username]++
	return true
}

// ReleaseSession decrements the active-session counter for the user.
func (s *Store) ReleaseSession(username string) {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if s.sessionCounts[username] > 0 {
		s.sessionCounts[username]--
		if s.sessionCounts[username] == 0 {
			delete(s.sessionCounts, username)
		}
	}
}

// RegisterSessionCleaner registers a live terminal session hub for lifecycle
// teardown on shutdown, logout, and server deletion.
func (s *Store) RegisterSessionCleaner(cleaner SessionCleaner) {
	if s == nil || cleaner == nil {
		return
	}
	s.cleanersMu.Lock()
	s.cleaners = append(s.cleaners, cleaner)
	s.cleanersMu.Unlock()
}

func (s *Store) snapshotCleaners() []SessionCleaner {
	s.cleanersMu.Lock()
	defer s.cleanersMu.Unlock()
	out := make([]SessionCleaner, len(s.cleaners))
	copy(out, s.cleaners)
	return out
}

// RevokeServer purges pending tickets and closes active/detached PTY shells
// associated with serverID.
func (s *Store) RevokeServer(serverID int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	for id, st := range s.tickets {
		if st.ticket.ServerID == serverID {
			delete(s.tickets, id)
		}
	}
	s.mu.Unlock()
	for _, cleaner := range s.snapshotCleaners() {
		cleaner.CloseForServer(serverID)
	}
}

// RevokeUser closes active/detached PTY shells associated with username.
func (s *Store) RevokeUser(username string) {
	if s == nil || username == "" {
		return
	}
	for _, cleaner := range s.snapshotCleaners() {
		cleaner.CloseForUser(username)
	}
}

// Close tears down all registered terminal session hubs during graceful
// shutdown so remote PTY shells do not leak.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	clear(s.tickets)
	s.mu.Unlock()
	var firstErr error
	for _, cleaner := range s.snapshotCleaners() {
		if err := cleaner.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Store) pruneExpired() {
	cutoff := time.Now().Add(-s.ttl)
	consumedCutoff := time.Now().Add(-s.ttl * 5)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, st := range s.tickets {
		if !st.used.Load() && st.ticket.CreatedAt.Before(cutoff) {
			delete(s.tickets, id)
		} else if st.used.Load() && st.ticket.CreatedAt.Before(consumedCutoff) {
			delete(s.tickets, id)
		}
	}
}

func newID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		slog.Error("terminalticket: failed to generate random ticket ID", slog.Any("error", err))
		return ""
	}
	return hex.EncodeToString(buf)
}
