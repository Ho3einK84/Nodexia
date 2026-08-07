package terminal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cwebsocket "github.com/coder/websocket"

	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/terminalticket"
)

type shellOutputRequest struct {
	data string
	done chan error
}

type resumableShellFixture struct {
	starts  atomic.Int32
	started chan struct{}
	output  chan shellOutputRequest
}

func (f *resumableShellFixture) OpenShell(ctx context.Context, _ sshclient.ConnectionRequest, pio sshclient.InteractiveIO) error {
	f.starts.Add(1)
	select {
	case <-f.started:
	default:
		close(f.started)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case request := <-f.output:
			_, err := pio.Stdout.Write([]byte(request.data))
			request.done <- err
		}
	}
}

func TestTerminalSessionReattachesWithoutRestartingShell(t *testing.T) {
	tickets := terminalticket.New(time.Minute)
	fixture := &resumableShellFixture{
		started: make(chan struct{}),
		output:  make(chan shellOutputRequest),
	}
	hub := newTerminalSessionHub(fixture, tickets)
	handler := wsHandler{
		deps:     module.Dependencies{TerminalTickets: tickets},
		sessions: hub,
	}
	mux := http.NewServeMux()
	mux.Handle("GET /servers/{id}/terminal/ws", handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	ticketID, err := tickets.Create(7, sshclient.ConnectionRequest{})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/servers/7/terminal/ws?ticket=" + ticketID
	dialOptions := &cwebsocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{server.URL}}}

	first, _, err := cwebsocket.Dial(context.Background(), wsURL, dialOptions)
	if err != nil {
		t.Fatalf("dial first viewer: %v", err)
	}
	if message := readTerminalMessage(t, first, "status"); message.State != "connected" {
		t.Fatalf("first status = %q, want connected", message.State)
	}
	select {
	case <-fixture.started:
	case <-time.After(2 * time.Second):
		t.Fatal("shell did not start")
	}

	if err := first.CloseNow(); err != nil {
		t.Fatalf("drop first viewer: %v", err)
	}
	waitForTerminalDetach(t, hub, ticketID)

	written := make(chan error, 1)
	select {
	case fixture.output <- shellOutputRequest{data: "output while backgrounded\n", done: written}:
	case <-time.After(2 * time.Second):
		t.Fatal("shell did not accept detached output fixture")
	}
	select {
	case err := <-written:
		if err != nil {
			t.Fatalf("buffer detached output: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shell did not finish buffering detached output")
	}

	second, _, err := cwebsocket.Dial(context.Background(), wsURL, dialOptions)
	if err != nil {
		t.Fatalf("dial resumed viewer: %v", err)
	}
	if message := readTerminalMessage(t, second, "status"); message.State != "connected" {
		t.Fatalf("resumed status = %q, want connected", message.State)
	}
	if message := readTerminalMessage(t, second, "output"); message.Data != "output while backgrounded\n" {
		t.Fatalf("resumed output = %q", message.Data)
	}
	if starts := fixture.starts.Load(); starts != 1 {
		t.Fatalf("SSH shell starts = %d, want 1", starts)
	}

	_ = second.Close(cwebsocket.StatusNormalClosure, "test complete")
	waitForTerminalRemoval(t, hub, ticketID)
}

func TestAppendTerminalOutputKeepsNewestBoundedData(t *testing.T) {
	initial := make([]byte, terminalResumeBufferBytes-2)
	for i := range initial {
		initial[i] = 'a'
	}
	got := appendTerminalOutput(initial, []byte("WXYZ"))
	if len(got) != terminalResumeBufferBytes {
		t.Fatalf("buffer length = %d, want %d", len(got), terminalResumeBufferBytes)
	}
	if tail := string(got[len(got)-4:]); tail != "WXYZ" {
		t.Fatalf("buffer tail = %q, want WXYZ", tail)
	}
}

type terminalTestMessage struct {
	Type  string `json:"type"`
	State string `json:"state"`
	Data  string `json:"data"`
}

func readTerminalMessage(t *testing.T, conn *cwebsocket.Conn, wantType string) terminalTestMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read %s message: %v", wantType, err)
		}
		var message terminalTestMessage
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatalf("decode terminal message: %v", err)
		}
		if message.Type == wantType {
			return message
		}
	}
}

func waitForTerminalDetach(t *testing.T, hub *terminalSessionHub, ticketID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		session, ok := hub.get(ticketID)
		if ok {
			session.mu.Lock()
			detached := session.conn == nil
			session.mu.Unlock()
			if detached {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("terminal session did not detach its first WebSocket")
}

func waitForTerminalRemoval(t *testing.T, hub *terminalSessionHub, ticketID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := hub.get(ticketID); !ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("terminal session was not removed after a clean close")
}
