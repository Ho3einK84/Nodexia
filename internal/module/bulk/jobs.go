package bulk

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"sync"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/view"
)

// Bulk actions run in the background so the POST request returns immediately
// and the result page polls for progress — a synchronous run would hold the
// HTTP request for minutes (package upgrades) and get cut off by the server's
// write timeout / the reverse proxy, surfacing as a 502 mid-action.
const (
	// finishedJobTTL is how long a completed job stays readable so the result
	// page can still be refreshed or revisited before it is pruned.
	finishedJobTTL = 30 * time.Minute
	// staleJobTTL caps how long an unfinished job may sit with NO row activity
	// before it is dropped (defensive; a job only stalls if its goroutine died
	// with the process). It is measured from the last row update, not creation:
	// a large fleet update (many servers × a 20-minute per-server timeout over 5
	// workers) legitimately runs for hours, but touches a row at least once per
	// action, so a live job never appears stale.
	staleJobTTL = 2 * time.Hour
)

// Per-row status values rendered by the result page.
const (
	statusPending = "pending"
	statusRunning = "running"
	statusOK      = "ok"
	statusFailed  = "failed"
	statusSkipped = "skipped"
)

// job tracks the live state of one background bulk run.
type job struct {
	id        string
	action    string
	createdAt time.Time

	mu         sync.Mutex
	rows       []view.BulkServerResultView
	finished   bool
	finishedAt time.Time
	// touchedAt is the last time a worker updated a row; the stale prune keys
	// off it so long-but-live runs are never dropped mid-flight.
	touchedAt time.Time
}

func (j *job) setRow(index int, row view.BulkServerResultView) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if index >= 0 && index < len(j.rows) {
		j.rows[index] = row
		j.touchedAt = time.Now()
	}
}

func (j *job) setStatus(index int, status string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if index >= 0 && index < len(j.rows) {
		j.rows[index].Status = status
		j.touchedAt = time.Now()
	}
}

func (j *job) finish() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.finished = true
	j.finishedAt = time.Now()
}

// snapshot returns a copy of the rows plus the finished flag, safe to render
// while workers keep mutating the job.
func (j *job) snapshot() ([]view.BulkServerResultView, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	rows := make([]view.BulkServerResultView, len(j.rows))
	copy(rows, j.rows)
	return rows, j.finished
}

// jobStore keeps in-flight and recently finished bulk jobs in memory.  Jobs
// are not persisted: after a restart the result page shows a friendly
// "expired" flash instead.
type jobStore struct {
	mu   sync.Mutex
	jobs map[string]*job
}

func newJobStore() *jobStore {
	return &jobStore{jobs: map[string]*job{}}
}

func (s *jobStore) create(action string, rows []view.BulkServerResultView) *job {
	j := &job{
		id:        randomJobID(),
		action:    action,
		createdAt: time.Now(),
		rows:      rows,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	s.jobs[j.id] = j
	return j
}

func (s *jobStore) get(id string) (*job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	j, ok := s.jobs[id]
	return j, ok
}

// pruneLocked drops expired jobs.  Must be called with s.mu held.
func (s *jobStore) pruneLocked() {
	now := time.Now()
	for id, j := range s.jobs {
		j.mu.Lock()
		lastActivity := j.touchedAt
		if lastActivity.IsZero() {
			lastActivity = j.createdAt
		}
		expired := (j.finished && now.Sub(j.finishedAt) > finishedJobTTL) ||
			(!j.finished && now.Sub(lastActivity) > staleJobTTL)
		j.mu.Unlock()
		if expired {
			delete(s.jobs, id)
		}
	}
}

func randomJobID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// Practically unreachable; the id is not a secret, only a lookup key.
		return "job-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf)
}
