package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	cwebsocket "github.com/coder/websocket"

	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/terminalticket"
)

const (
	// terminalResumeGrace bounds how long a disconnected PTY is retained. This
	// covers ordinary mobile/PWA suspension without leaking abandoned SSH shells
	// indefinitely after a browser process is killed.
	terminalResumeGrace = 10 * time.Minute

	// terminalResumeBufferBytes bounds output retained while no browser
	// WebSocket is attached. The newest output is kept so a resumed terminal can
	// show the prompt and the most recent command result.
	terminalResumeBufferBytes = 1024 * 1024
)

type shellOpener interface {
	OpenShell(context.Context, sshclient.ConnectionRequest, sshclient.InteractiveIO) error
}

// terminalSessionHub owns live SSH shells independently from the transient
// WebSocket request used to view them. A consumed ticket remains the opaque
// resume handle for exactly one shell; it cannot create a second shell.
type terminalSessionHub struct {
	mu       sync.Mutex
	sessions map[string]*terminalSession
	ssh      shellOpener
	tickets  *terminalticket.Store
}

func newTerminalSessionHub(ssh shellOpener, tickets *terminalticket.Store) *terminalSessionHub {
	return &terminalSessionHub{
		sessions: make(map[string]*terminalSession),
		ssh:      ssh,
		tickets:  tickets,
	}
}

func (h *terminalSessionHub) get(id string) (*terminalSession, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sessions[id]
	return s, ok
}

func (h *terminalSessionHub) create(ticket terminalticket.Ticket, username string) *terminalSession {
	ctx, cancel := context.WithCancel(context.Background())
	stdinR, stdinW := io.Pipe()
	s := &terminalSession{
		id:       ticket.ID,
		serverID: ticket.ServerID,
		username: username,
		req:      ticket.Req,
		hub:      h,
		ctx:      ctx,
		cancel:   cancel,
		stdinR:   stdinR,
		stdinW:   stdinW,
		resizeCh: make(chan sshclient.ResizeRequest, 8),
	}
	s.output = &resumableOutputWriter{session: s}

	h.mu.Lock()
	h.sessions[ticket.ID] = s
	h.mu.Unlock()
	return s
}

func (h *terminalSessionHub) remove(s *terminalSession) {
	h.mu.Lock()
	if h.sessions[s.id] == s {
		delete(h.sessions, s.id)
	}
	h.mu.Unlock()
	h.tickets.Release(s.id)
	h.tickets.ReleaseSession(s.username)
}

type terminalSession struct {
	id       string
	serverID int64
	username string
	req      sshclient.ConnectionRequest
	hub      *terminalSessionHub

	ctx    context.Context
	cancel context.CancelFunc
	stdinR *io.PipeReader
	stdinW *io.PipeWriter

	resizeCh chan sshclient.ResizeRequest
	output   *resumableOutputWriter
	start    sync.Once
	finish   sync.Once

	// writeMu serializes all writes and attachment swaps. coder/websocket does
	// not permit concurrent writers on one connection.
	writeMu sync.Mutex
	mu      sync.Mutex
	conn    *cwebsocket.Conn
	buffer  []byte
	ended   bool
	expiry  *time.Timer
}

func (s *terminalSession) startShell() {
	s.start.Do(func() {
		go func() {
			err := s.hub.ssh.OpenShell(s.ctx, s.req, sshclient.InteractiveIO{
				Stdin:  s.stdinR,
				Stdout: s.output,
				Stderr: s.output,
				Rows:   24,
				Cols:   80,
				Resize: s.resizeCh,
			})
			s.finishShell(err)
		}()
	})
}

func (s *terminalSession) finishShell(shellErr error) {
	s.finish.Do(func() {
		if shellErr != nil &&
			!errors.Is(shellErr, context.Canceled) &&
			!errors.Is(shellErr, context.DeadlineExceeded) {
			_ = s.writeControl(struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			}{"error", "ssh: " + shellErr.Error()})
		}

		s.cancel()
		_ = s.stdinW.Close()
		_ = s.stdinR.Close()

		s.writeMu.Lock()
		s.mu.Lock()
		s.ended = true
		if s.expiry != nil {
			s.expiry.Stop()
			s.expiry = nil
		}
		conn := s.conn
		s.conn = nil
		s.mu.Unlock()
		if conn != nil {
			_ = conn.Close(cwebsocket.StatusNormalClosure, "session ended")
		}
		s.writeMu.Unlock()

		s.hub.remove(s)
	})
}

func (s *terminalSession) stop() {
	s.cancel()
	_ = s.stdinW.Close()
}

// attach replaces a stale viewer without restarting the SSH shell, sends the
// live status, and then replays bounded output captured while disconnected.
func (s *terminalSession) attach(conn *cwebsocket.Conn) bool {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return false
	}
	old := s.conn
	s.conn = conn
	if s.expiry != nil {
		s.expiry.Stop()
		s.expiry = nil
	}
	buffered := append([]byte(nil), s.buffer...)
	s.mu.Unlock()

	if old != nil && old != conn {
		_ = old.CloseNow()
	}

	status, _ := json.Marshal(struct {
		Type  string `json:"type"`
		State string `json:"state"`
	}{"status", "connected"})
	if err := s.writeRaw(conn, status); err != nil {
		s.detachLocked(conn)
		return false
	}

	for len(buffered) > 0 {
		chunk := buffered
		if len(chunk) > wsOutputChunkBytes {
			chunk = buffered[:wsOutputChunkBytes]
		}
		payload, _ := json.Marshal(struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}{"output", string(chunk)})
		if err := s.writeRaw(conn, payload); err != nil {
			s.detachLocked(conn)
			return false
		}
		buffered = buffered[len(chunk):]
	}

	s.mu.Lock()
	s.buffer = nil
	s.mu.Unlock()
	return true
}

func (s *terminalSession) detach(conn *cwebsocket.Conn) {
	s.mu.Lock()
	detached := s.detachLockedWithMutex(conn)
	s.mu.Unlock()
	if detached {
		_ = conn.CloseNow()
	}
}

// detachLocked is called with writeMu held, but not mu.
func (s *terminalSession) detachLocked(conn *cwebsocket.Conn) {
	s.mu.Lock()
	detached := s.detachLockedWithMutex(conn)
	s.mu.Unlock()
	if detached {
		_ = conn.CloseNow()
	}
}

func (s *terminalSession) detachLockedWithMutex(conn *cwebsocket.Conn) bool {
	if s.conn != conn {
		return false
	}
	s.conn = nil
	if !s.ended {
		if s.expiry != nil {
			s.expiry.Stop()
		}
		s.expiry = time.AfterFunc(terminalResumeGrace, s.stop)
	}
	return true
}

func (s *terminalSession) writeOutput(p []byte) error {
	for len(p) > 0 {
		chunk := p
		if len(chunk) > wsOutputChunkBytes {
			chunk = p[:wsOutputChunkBytes]
		}
		if err := s.writeOutputChunk(chunk); err != nil {
			return err
		}
		p = p[len(chunk):]
	}
	return nil
}

func (s *terminalSession) writeOutputChunk(chunk []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	conn := s.conn
	if conn == nil {
		s.buffer = appendTerminalOutput(s.buffer, chunk)
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	payload, _ := json.Marshal(struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}{"output", string(chunk)})
	if err := s.writeRaw(conn, payload); err != nil {
		s.mu.Lock()
		s.buffer = appendTerminalOutput(s.buffer, chunk)
		detached := s.detachLockedWithMutex(conn)
		s.mu.Unlock()
		if detached {
			_ = conn.CloseNow()
		}
	}
	// A viewer loss is recoverable and must not terminate the SSH shell.
	return nil
}

func (s *terminalSession) writeControl(message any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	conn := s.conn
	ended := s.ended
	s.mu.Unlock()
	if ended || conn == nil {
		return io.ErrClosedPipe
	}
	if err := s.writeRaw(conn, payload); err != nil {
		s.detachLocked(conn)
		return err
	}
	return nil
}

func (s *terminalSession) writeHeartbeat(conn *cwebsocket.Conn) error {
	payload, _ := json.Marshal(struct {
		Type string `json:"type"`
	}{"heartbeat"})

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	active := !s.ended && s.conn == conn
	s.mu.Unlock()
	if !active {
		return io.ErrClosedPipe
	}
	if err := s.writeRaw(conn, payload); err != nil {
		s.detachLocked(conn)
		return err
	}
	return nil
}

func (s *terminalSession) writeRaw(conn *cwebsocket.Conn, payload []byte) error {
	writeCtx, cancel := context.WithTimeout(s.ctx, wsWriteTimeout)
	defer cancel()
	return conn.Write(writeCtx, cwebsocket.MessageText, payload)
}

type resumableOutputWriter struct {
	session *terminalSession
}

func (w *resumableOutputWriter) Write(p []byte) (int, error) {
	if err := w.session.writeOutput(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func appendTerminalOutput(buffer, output []byte) []byte {
	if len(output) >= terminalResumeBufferBytes {
		return append(buffer[:0], output[len(output)-terminalResumeBufferBytes:]...)
	}
	overflow := len(buffer) + len(output) - terminalResumeBufferBytes
	if overflow > 0 {
		copy(buffer, buffer[overflow:])
		buffer = buffer[:len(buffer)-overflow]
	}
	return append(buffer, output...)
}
