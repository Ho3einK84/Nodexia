package commandstream

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
	"unicode/utf8"

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

	minPruneInterval = 30 * time.Second
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

type streamBuffer struct {
	stdout []byte
	stderr []byte
	notify chan struct{}
}

type Store struct {
	mu        sync.RWMutex
	sessions  map[string]Snapshot
	buffers   map[string]*streamBuffer
	ttl       time.Duration
	lastPrune time.Time
}

func New(ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}

	return &Store{
		sessions: make(map[string]Snapshot),
		buffers:  make(map[string]*streamBuffer),
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
	s.maybePrune()

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
	if s.buffers == nil {
		s.buffers = make(map[string]*streamBuffer)
	}
	s.sessions[snapshot.ID] = snapshot
	s.buffers[snapshot.ID] = &streamBuffer{
		notify: make(chan struct{}),
	}
	s.mu.Unlock()
	return snapshot
}

func (s *Store) AppendStdout(id string, chunk string) {
	chunk = ansi.Strip(chunk)
	if chunk == "" {
		return
	}
	s.appendStream(id, true, chunk)
}

func (s *Store) AppendStderr(id string, chunk string) {
	chunk = ansi.Strip(chunk)
	if chunk == "" {
		return
	}
	s.appendStream(id, false, chunk)
}

func (s *Store) appendStream(id string, isStdout bool, chunk string) {
	if id == "" || chunk == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot, ok := s.sessions[id]
	if !ok {
		return
	}
	buf := s.ensureBufferLocked(id)
	if isStdout {
		buf.stdout = appendCappedBytes(buf.stdout, chunk)
	} else {
		buf.stderr = appendCappedBytes(buf.stderr, chunk)
	}
	snapshot.UpdatedAt = time.Now().UTC()
	s.sessions[id] = snapshot
	signalLocked(buf)
}

func appendCappedBytes(buf []byte, chunk string) []byte {
	if len(buf) >= maxStreamOutputBytes {
		return buf
	}
	remaining := maxStreamOutputBytes - len(buf)
	if len(chunk) > remaining {
		cut := remaining
		for cut > 0 && !utf8.RuneStart(chunk[cut]) {
			cut--
		}
		buf = append(buf, chunk[:cut]...)
		return append(buf, "\n[output truncated at 1 MB]"...)
	}
	return append(buf, chunk...)
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

	now := time.Now().UTC()
	s.mu.RLock()
	snapshot, ok := s.sessions[id]
	if !ok || s.isExpiredLocked(snapshot, now) {
		s.mu.RUnlock()
		return Snapshot{}, false
	}
	if buf := s.buffers[id]; buf != nil {
		if len(buf.stdout) > 0 {
			snapshot.Stdout = string(buf.stdout)
		}
		if len(buf.stderr) > 0 {
			snapshot.Stderr = string(buf.stderr)
		}
	}
	s.mu.RUnlock()
	return snapshot, true
}

// readDelta reads only the unread tail of stdout/stderr along with a broadcast
// channel that closes on the next state change. This avoids copying the entire
// accumulated output buffer on every SSE event.
func (s *Store) readDelta(id string, sentOut, sentErr int) (snap Snapshot, outDelta, errDelta string, nextOut, nextErr int, waitCh <-chan struct{}, ok bool) {
	if id == "" {
		return Snapshot{}, "", "", sentOut, sentErr, nil, false
	}

	now := time.Now().UTC()
	s.mu.RLock()
	snapshot, exists := s.sessions[id]
	if !exists || s.isExpiredLocked(snapshot, now) {
		s.mu.RUnlock()
		return Snapshot{}, "", "", sentOut, sentErr, nil, false
	}

	nextOut = sentOut
	nextErr = sentErr
	if buf := s.buffers[id]; buf != nil {
		if len(buf.stdout) > sentOut {
			outDelta = string(buf.stdout[sentOut:])
			nextOut = len(buf.stdout)
		}
		if len(buf.stderr) > sentErr {
			errDelta = string(buf.stderr[sentErr:])
			nextErr = len(buf.stderr)
		}
		waitCh = buf.notify
	} else {
		if len(snapshot.Stdout) > sentOut {
			outDelta = snapshot.Stdout[sentOut:]
			nextOut = len(snapshot.Stdout)
		}
		if len(snapshot.Stderr) > sentErr {
			errDelta = snapshot.Stderr[sentErr:]
			nextErr = len(snapshot.Stderr)
		}
	}
	s.mu.RUnlock()
	return snapshot, outDelta, errDelta, nextOut, nextErr, waitCh, true
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
	signalLocked(s.buffers[id])
}

func (s *Store) ensureBufferLocked(id string) *streamBuffer {
	if s.buffers == nil {
		s.buffers = make(map[string]*streamBuffer)
	}
	buf, ok := s.buffers[id]
	if !ok || buf == nil {
		buf = &streamBuffer{notify: make(chan struct{})}
		s.buffers[id] = buf
	}
	return buf
}

func signalLocked(buf *streamBuffer) {
	if buf == nil || buf.notify == nil {
		return
	}
	close(buf.notify)
	buf.notify = make(chan struct{})
}

func (s *Store) isExpiredLocked(snapshot Snapshot, now time.Time) bool {
	timestamp := snapshot.UpdatedAt
	if timestamp.IsZero() {
		timestamp = snapshot.StartedAt
	}
	if timestamp.IsZero() {
		return false
	}
	if snapshot.Status == StatusRunning {
		return timestamp.Before(now.Add(-runningSessionMaxAge))
	}
	return timestamp.Before(now.Add(-s.ttl))
}

func (s *Store) maybePrune() {
	s.mu.RLock()
	shouldPrune := s.lastPrune.IsZero() || time.Since(s.lastPrune) >= minPruneInterval
	s.mu.RUnlock()
	if shouldPrune {
		s.pruneExpired()
	}
}

func (s *Store) pruneExpired() {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastPrune = now
	for id, snapshot := range s.sessions {
		if s.isExpiredLocked(snapshot, now) {
			delete(s.sessions, id)
			if buf := s.buffers[id]; buf != nil {
				if buf.notify != nil {
					close(buf.notify)
					buf.notify = nil
				}
				delete(s.buffers, id)
			}
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
