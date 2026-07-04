package commandstream

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/ansi"
)

const (
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"

	maxStreamOutputBytes = 1 << 20 // 1 MiB per stdout/stderr stream

	// runningSessionMaxAge is the hard retention cap for sessions still marked
	// running. A running command that stays quiet for longer than the normal TTL
	// (e.g. a silent long package upgrade with a generous custom timeout) must
	// not be pruned mid-run — Complete/Fail would then no-op and the result page
	// would report the session as expired even though it finished. Every run
	// path is bounded by a command timeout, so this cap is purely defensive.
	runningSessionMaxAge = 24 * time.Hour
)

type Snapshot struct {
	ID          string
	ServerID    int64
	Command     string
	Status      string
	Stdout      string
	Stderr      string
	Error       string
	StartedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt time.Time
	ExitCode    *int
	HistoryID   int64
}

type Store struct {
	mu       sync.RWMutex
	sessions map[string]Snapshot
	ttl      time.Duration
}

func New(ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}

	return &Store{
		sessions: make(map[string]Snapshot),
		ttl:      ttl,
	}
}

func (s *Store) ActiveCount() int {
	if s == nil {
		return 0
	}

	s.pruneExpired()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

func (s *Store) Create(serverID int64, command string) Snapshot {
	s.pruneExpired()

	now := time.Now().UTC()
	snapshot := Snapshot{
		ID:        newID(),
		ServerID:  serverID,
		Command:   command,
		Status:    StatusRunning,
		StartedAt: now,
		UpdatedAt: now,
	}

	s.mu.Lock()
	s.sessions[snapshot.ID] = snapshot
	s.mu.Unlock()
	return snapshot
}

func (s *Store) AppendStdout(id string, chunk string) {
	chunk = ansi.Strip(chunk)
	if chunk == "" {
		return
	}
	s.update(id, func(snapshot *Snapshot) {
		snapshot.Stdout = appendCapped(snapshot.Stdout, chunk)
	})
}

func (s *Store) AppendStderr(id string, chunk string) {
	chunk = ansi.Strip(chunk)
	if chunk == "" {
		return
	}
	s.update(id, func(snapshot *Snapshot) {
		snapshot.Stderr = appendCapped(snapshot.Stderr, chunk)
	})
}

func appendCapped(buf, chunk string) string {
	if len(buf) >= maxStreamOutputBytes {
		return buf
	}
	remaining := maxStreamOutputBytes - len(buf)
	if len(chunk) > remaining {
		return buf + chunk[:remaining] + "\n[output truncated at 1 MiB]"
	}
	return buf + chunk
}

func (s *Store) Complete(id string, exitCode *int, completedAt time.Time, historyID int64) {
	s.update(id, func(snapshot *Snapshot) {
		snapshot.Status = StatusCompleted
		snapshot.ExitCode = cloneExitCode(exitCode)
		snapshot.CompletedAt = normalizeTime(completedAt)
		snapshot.HistoryID = historyID
	})
}

func (s *Store) Fail(id string, exitCode *int, completedAt time.Time, err error, historyID int64) {
	s.update(id, func(snapshot *Snapshot) {
		snapshot.Status = StatusFailed
		snapshot.ExitCode = cloneExitCode(exitCode)
		snapshot.CompletedAt = normalizeTime(completedAt)
		snapshot.HistoryID = historyID
		if err != nil {
			snapshot.Error = err.Error()
		}
	})
}

func (s *Store) Get(id string) (Snapshot, bool) {
	if id == "" {
		return Snapshot{}, false
	}

	s.pruneExpired()

	s.mu.RLock()
	snapshot, ok := s.sessions[id]
	s.mu.RUnlock()
	if !ok {
		return Snapshot{}, false
	}

	return snapshot, true
}

func (s *Store) update(id string, update func(snapshot *Snapshot)) {
	if id == "" || update == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot, ok := s.sessions[id]
	if !ok {
		return
	}

	update(&snapshot)
	snapshot.UpdatedAt = time.Now().UTC()
	s.sessions[id] = snapshot
}

func (s *Store) pruneExpired() {
	now := time.Now().UTC()
	cutoff := now.Add(-s.ttl)
	runningCutoff := now.Add(-runningSessionMaxAge)

	s.mu.Lock()
	defer s.mu.Unlock()

	for id, snapshot := range s.sessions {
		timestamp := snapshot.UpdatedAt
		if timestamp.IsZero() {
			timestamp = snapshot.StartedAt
		}
		if timestamp.IsZero() {
			continue
		}
		// Running sessions are exempt from the normal TTL (quiet ≠ finished) and
		// only fall to the defensive hard cap; finished ones expire on the TTL.
		if snapshot.Status == StatusRunning {
			if timestamp.Before(runningCutoff) {
				delete(s.sessions, id)
			}
			continue
		}
		if timestamp.Before(cutoff) {
			delete(s.sessions, id)
		}
	}
}

func cloneExitCode(value *int) *int {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func normalizeTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func newID() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format("20060102150405.000000000")))
	}
	return hex.EncodeToString(buffer)
}
