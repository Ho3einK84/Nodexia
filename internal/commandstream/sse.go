package commandstream

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/sse"
)

// ssePollInterval is how often the SSE handler re-reads the snapshot for new
// output. This is a cheap in-memory read; it replaces the browser reloading the
// whole page every 2 seconds.
const ssePollInterval = 100 * time.Millisecond

// sseKeepAlive bounds how long the stream can sit silent before a comment frame
// is sent, so idle reverse proxies do not close the connection.
const sseKeepAlive = 15 * time.Second

// terminalPayload is the JSON body of the "done"/"error" SSE events.
type terminalPayload struct {
	Status   string `json:"status"`
	ExitCode string `json:"exitCode"`
	Duration string `json:"duration"`
	Message  string `json:"message,omitempty"`
}

// ServeSSE streams a session's output to the client as Server-Sent Events until
// the session leaves the running state (or the client disconnects). Callers
// must validate ownership (server scope) before invoking this.
//
// Output is streamed from offset 0 on connect — the browser resets its view on
// each EventSource (re)connect — so a reconnect after a network blip re-syncs
// the full buffer without duplicating lines.
func (s *Store) ServeSSE(w http.ResponseWriter, r *http.Request, id string) {
	out := sse.NewWriter(w)
	ctx := r.Context()

	var sentOut, sentErr int
	emit := func(snap Snapshot) bool {
		if len(snap.Stdout) > sentOut {
			if out.Event("output", snap.Stdout[sentOut:]) != nil {
				return false
			}
			sentOut = len(snap.Stdout)
		}
		if len(snap.Stderr) > sentErr {
			if out.Event("stderr", snap.Stderr[sentErr:]) != nil {
				return false
			}
			sentErr = len(snap.Stderr)
		}
		return true
	}

	snap, ok := s.Get(id)
	if !ok {
		_ = out.Event("error", `{"status":"failed","message":"This live session is no longer available."}`)
		return
	}
	if !emit(snap) {
		return
	}
	if snap.Status != StatusRunning {
		event, data := terminalFrame(snap)
		_ = out.Event(event, data)
		return
	}

	poll := time.NewTicker(ssePollInterval)
	defer poll.Stop()
	keepalive := time.NewTicker(sseKeepAlive)
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
			snap, ok := s.Get(id)
			if !ok {
				_ = out.Event("error", `{"status":"failed","message":"This live session expired."}`)
				return
			}
			if !emit(snap) {
				return
			}
			if snap.Status != StatusRunning {
				event, data := terminalFrame(snap)
				_ = out.Event(event, data)
				return
			}
		}
	}
}

// terminalFrame builds the closing SSE event for a finished session. A failed
// session is reported as an "error" event carrying the message; a successful
// one as "done".
func terminalFrame(snap Snapshot) (event, data string) {
	durationEnd := snap.CompletedAt
	if durationEnd.IsZero() {
		durationEnd = time.Now().UTC()
	}
	payload := terminalPayload{
		Status:   snap.Status,
		ExitCode: formatExitCode(snap.ExitCode),
		Duration: formatDuration(durationEnd.Sub(snap.StartedAt)),
	}

	event = "done"
	if snap.Status == StatusFailed {
		event = "error"
		payload.Message = snap.Error
		if payload.Message == "" {
			payload.Message = "The operation failed."
		}
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return event, `{"status":"` + snap.Status + `"}`
	}
	return event, string(encoded)
}

func formatExitCode(value *int) string {
	if value == nil {
		return "n/a"
	}
	return strconv.Itoa(*value)
}

func formatDuration(value time.Duration) string {
	if value <= 0 {
		return "-"
	}
	return value.Round(time.Millisecond).String()
}
