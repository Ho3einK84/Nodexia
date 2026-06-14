package nodes

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/sse"
)

// nodeSSEPollInterval mirrors the command stream: re-read the in-memory job
// every 100ms for new output instead of reloading the whole page every 2s.
const nodeSSEPollInterval = 100 * time.Millisecond

// nodeSSEKeepAlive sends a comment frame on idle streams so reverse proxies do
// not close a connection that is quietly waiting for the next chunk.
const nodeSSEKeepAlive = 15 * time.Second

// NodeStreamEvents serves GET /servers/{id}/nodes/stream/{stream}/events — the
// SSE feed for a running node action (update / uninstall / …). Node actions run
// as commandstream sessions, so this defers to the shared store streamer after
// verifying the stream belongs to this server.
func (h *Handlers) NodeStreamEvents(w http.ResponseWriter, r *http.Request) {
	serverID, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if h.deps.CommandStreams == nil {
		http.Error(w, "live stream store unavailable", http.StatusServiceUnavailable)
		return
	}

	streamID := strings.TrimSpace(r.PathValue("stream"))
	snapshot, found := h.deps.CommandStreams.Get(streamID)
	if !found || snapshot.ServerID != serverID {
		http.Error(w, "live stream not found", http.StatusNotFound)
		return
	}

	h.deps.CommandStreams.ServeSSE(w, r, streamID)
}

// InstallEvents serves GET /servers/{id}/nodes/install/{job}/events — the SSE
// feed for a running PasarGuard install. The job carries one combined output
// buffer (stdout + stderr merged by the installer). On completion the client
// reloads the page so the server can render the memory-only registration card
// (API key + certificate are never embedded in the event stream).
func (h *Handlers) InstallEvents(w http.ResponseWriter, r *http.Request) {
	serverID, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}

	job, found := h.installs.get(strings.TrimSpace(r.PathValue("job")))
	if !found || job.serverID != serverID {
		http.Error(w, "install session not found", http.StatusNotFound)
		return
	}

	out := sse.NewWriter(w)
	ctx := r.Context()

	var sent int
	emit := func(snap installSnapshot) bool {
		if len(snap.Output) > sent {
			if out.Event("output", snap.Output[sent:]) != nil {
				return false
			}
			sent = len(snap.Output)
		}
		return true
	}

	snap := job.snapshot()
	if !emit(snap) {
		return
	}
	if snap.Status != installStatusRunning {
		event, data := installTerminalFrame(snap)
		_ = out.Event(event, data)
		return
	}

	poll := time.NewTicker(nodeSSEPollInterval)
	defer poll.Stop()
	keepalive := time.NewTicker(nodeSSEKeepAlive)
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
			snap := job.snapshot()
			if !emit(snap) {
				return
			}
			if snap.Status != installStatusRunning {
				event, data := installTerminalFrame(snap)
				_ = out.Event(event, data)
				return
			}
		}
	}
}

// installTerminalFrame builds the closing SSE event for a finished install.
func installTerminalFrame(snap installSnapshot) (event, data string) {
	durationEnd := snap.FinishedAt
	if durationEnd.IsZero() {
		durationEnd = time.Now().UTC()
	}
	payload := map[string]string{
		"status":   snap.Status,
		"duration": formatDuration(durationEnd.Sub(snap.CreatedAt)),
	}

	event = "done"
	if snap.Status == installStatusFailed {
		event = "error"
		message := snap.Error
		if message == "" {
			message = "The install failed."
		}
		payload["message"] = message
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return event, `{"status":"` + snap.Status + `"}`
	}
	return event, string(encoded)
}
