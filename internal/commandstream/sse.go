package commandstream

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/sse"
)

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
// Delivery is event-driven via the session's broadcast channel rather than
// polling on a fixed ticker, so chunks flush immediately and idle streams do no
// background work between keep-alives.
func (s *Store) ServeSSE(w http.ResponseWriter, r *http.Request, id string) {
	out := sse.NewWriter(w)
	ctx := r.Context()

	var sentOut, sentErr int
	emitDelta := func(outDelta, errDelta string) bool {
		if outDelta != "" {
			if out.Event("output", outDelta) != nil {
				return false
			}
		}
		if errDelta != "" {
			if out.Event("stderr", errDelta) != nil {
				return false
			}
		}
		return true
	}

	snap, outDelta, errDelta, nextOut, nextErr, waitCh, ok := s.readDelta(id, sentOut, sentErr)
	if !ok {
		_ = out.Event("error", `{"status":"failed","message":"This live session is no longer available."}`)
		return
	}
	if !emitDelta(outDelta, errDelta) {
		return
	}
	sentOut, sentErr = nextOut, nextErr
	if snap.Status != StatusRunning {
		event, data := terminalFrame(snap)
		_ = out.Event(event, data)
		return
	}

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
		case <-waitCh:
			snap, outDelta, errDelta, nextOut, nextErr, waitCh, ok = s.readDelta(id, sentOut, sentErr)
			if !ok {
				_ = out.Event("error", `{"status":"failed","message":"This live session expired."}`)
				return
			}
			if !emitDelta(outDelta, errDelta) {
				return
			}
			sentOut, sentErr = nextOut, nextErr
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
