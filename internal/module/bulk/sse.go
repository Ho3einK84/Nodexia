package bulk

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/sse"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

const (
	// Server responses arrive over seconds, so a slightly coarser poll than the
	// command streams is plenty while still feeling live.
	bulkSSEPollInterval = 150 * time.Millisecond
	bulkSSEKeepAlive    = 15 * time.Second
)

// JobEventsHandler serves GET /servers/bulk/jobs/{job}/events: a per-server
// progress stream for a running bulk action. Each server row is pushed as a
// "row" event whenever its status/exit/reason changes, and a final "done" event
// carries the summary counts so the page header can settle without a reload.
type JobEventsHandler struct {
	deps module.Dependencies
	jobs *jobStore
}

func newJobEventsHandler(deps module.Dependencies, jobs *jobStore) JobEventsHandler {
	return JobEventsHandler{deps: deps, jobs: jobs}
}

// bulkRowEvent is the JSON body of a "row" event (one server's current state).
type bulkRowEvent struct {
	Index    int    `json:"index"`
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	ExitCode string `json:"exitCode"`
	Reason   string `json:"reason"`
}

// bulkDoneEvent is the JSON body of the closing "done" event.
type bulkDoneEvent struct {
	Action     string `json:"action"`
	OK         int    `json:"ok"`
	Failed     int    `json:"failed"`
	Skipped    int    `json:"skipped"`
	InProgress int    `json:"inProgress"`
	Total      int    `json:"total"`
}

func (h JobEventsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	job, ok := h.jobs.get(strings.TrimSpace(r.PathValue("job")))
	if !ok {
		http.Error(w, "bulk job not found", http.StatusNotFound)
		return
	}

	out := sse.NewWriter(w)
	ctx := r.Context()

	// Only push rows whose rendered state changed since the last frame.
	lastSent := make(map[int]string)
	emit := func(rows []view.BulkServerResultView) bool {
		for i, row := range rows {
			encoded, err := json.Marshal(bulkRowEvent{
				Index:    i,
				ID:       row.ID,
				Name:     row.Name,
				Status:   row.Status,
				ExitCode: row.ExitCode,
				Reason:   row.Reason,
			})
			if err != nil {
				continue
			}
			frame := string(encoded)
			if lastSent[i] == frame {
				continue
			}
			if out.Event("row", frame) != nil {
				return false
			}
			lastSent[i] = frame
		}
		return true
	}

	sendDone := func(action string, rows []view.BulkServerResultView) {
		summary := summarize(action, rows)
		encoded, err := json.Marshal(bulkDoneEvent{
			Action:     action,
			OK:         summary.OKCount,
			Failed:     summary.FailedCount,
			Skipped:    summary.SkippedCount,
			InProgress: summary.InProgressCount,
			Total:      summary.Total,
		})
		if err == nil {
			_ = out.Event("done", string(encoded))
		}
	}

	rows, finished := job.snapshot()
	if !emit(rows) {
		return
	}
	if finished {
		sendDone(job.action, rows)
		return
	}

	poll := time.NewTicker(bulkSSEPollInterval)
	defer poll.Stop()
	keepalive := time.NewTicker(bulkSSEKeepAlive)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			if out.Comment("keep-alive") != nil {
				return
			}
		case <-poll.C:
			rows, finished := job.snapshot()
			if !emit(rows) {
				return
			}
			if finished {
				sendDone(job.action, rows)
				return
			}
		}
	}
}
