package sshclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	xssh "golang.org/x/crypto/ssh"
)

func TestBatchSessionReusesSingleConnection(t *testing.T) {
	listener, password, connCount, _ := startMultiSessionSSHServer(t, false)
	address := listener.Addr().(*net.TCPAddr)

	service := &Service{
		connectTimeout: 2 * time.Second,
		commandTimeout: 2 * time.Second,
		hostKeyPolicy:  "insecure",
	}

	batch, err := service.OpenBatchSession(context.Background(), ConnectionRequest{
		Host:     address.IP.String(),
		Port:     address.Port,
		Username: "nodexia",
		AuthMode: "password",
		Password: password,
	})
	if err != nil {
		t.Fatalf("OpenBatchSession: %v", err)
	}
	defer func() { _ = batch.Close() }()

	if batch.RemoteAddress() == "" {
		t.Fatal("expected non-empty RemoteAddress")
	}
	if batch.RTT() < 0 {
		t.Fatalf("unexpected negative RTT: %v", batch.RTT())
	}

	for _, cmd := range []string{"probe-one", "probe-two", "probe-three"} {
		res, err := batch.RunCommand(context.Background(), CommandRequest{Command: cmd})
		if err != nil {
			t.Fatalf("RunCommand(%q): %v", cmd, err)
		}
		if res.ExitCode == nil || *res.ExitCode != 0 {
			t.Fatalf("RunCommand(%q) exit = %v, want 0", cmd, res.ExitCode)
		}
		if strings.TrimSpace(res.Stdout) != "ok:"+cmd {
			t.Fatalf("RunCommand(%q) stdout = %q, want %q", cmd, res.Stdout, "ok:"+cmd)
		}
	}

	if got := connCount.Load(); got != 1 {
		t.Fatalf("accepted TCP connections = %d, want 1", got)
	}
	if err := batch.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := batch.RunCommand(context.Background(), CommandRequest{Command: "after-close"}); err == nil {
		t.Fatal("expected error calling RunCommand after Close")
	}
}

func TestStreamCommandCancelUnblocksHalfOpenSession(t *testing.T) {
	listener, password, _, doneCh := startMultiSessionSSHServer(t, true)
	address := listener.Addr().(*net.TCPAddr)

	service := &Service{
		connectTimeout: 2 * time.Second,
		commandTimeout: 5 * time.Second,
		hostKeyPolicy:  "insecure",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := service.StreamCommand(ctx, CommandRequest{
		ConnectionRequest: ConnectionRequest{
			Host:     address.IP.String(),
			Port:     address.Port,
			Username: "nodexia",
			AuthMode: "password",
			Password: password,
		},
		Command:        "hang-forever",
		CommandTimeout: 5 * time.Second,
	}, StreamHandlers{})
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("expected timeout/cancellation error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected deadline/canceled error, got: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("StreamCommand took too long to abort (%v); transport may still be open", elapsed)
	}
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("remote handler did not observe closed transport")
	}
}

func startMultiSessionSSHServer(t *testing.T, hangExec bool) (net.Listener, string, *atomic.Int32, <-chan struct{}) {
	t.Helper()

	_, hostPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate SSH host key: %v", err)
	}
	hostSigner, err := xssh.NewSignerFromKey(hostPrivate)
	if err != nil {
		t.Fatalf("create SSH host signer: %v", err)
	}

	const password = "batch-test-password"
	serverConfig := &xssh.ServerConfig{
		PasswordCallback: func(_ xssh.ConnMetadata, supplied []byte) (*xssh.Permissions, error) {
			if string(supplied) != password {
				return nil, errors.New("bad password")
			}
			return &xssh.Permissions{}, nil
		},
	}
	serverConfig.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	var connCount atomic.Int32
	doneCh := make(chan struct{}, 1)

	go func() {
		for {
			rawConn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			connCount.Add(1)
			go func(conn net.Conn) {
				defer func() { _ = conn.Close() }()
				serverConn, channels, globalRequests, handshakeErr := xssh.NewServerConn(conn, serverConfig)
				if handshakeErr != nil {
					return
				}
				defer func() { _ = serverConn.Close() }()
				go xssh.DiscardRequests(globalRequests)

				for newChannel := range channels {
					if newChannel.ChannelType() != "session" {
						_ = newChannel.Reject(xssh.UnknownChannelType, "session required")
						continue
					}
					channel, requests, chErr := newChannel.Accept()
					if chErr != nil {
						return
					}
					go func(ch xssh.Channel, reqs <-chan *xssh.Request) {
						defer func() { _ = ch.Close() }()
						for req := range reqs {
							if req.Type != "exec" {
								_ = req.Reply(false, nil)
								continue
							}
							var payload struct{ Command string }
							if err := xssh.Unmarshal(req.Payload, &payload); err != nil {
								_ = req.Reply(false, nil)
								continue
							}
							_ = req.Reply(true, nil)
							if hangExec {
								// Wait until transport closes cleanly via client.Close().
								_ = serverConn.Wait()
								select {
								case doneCh <- struct{}{}:
								default:
								}
								return
							}
							_, _ = ch.Write([]byte("ok:" + payload.Command + "\n"))
							_, _ = ch.SendRequest("exit-status", false, xssh.Marshal(struct{ Status uint32 }{Status: 0}))
							return
						}
					}(channel, requests)
				}
			}(rawConn)
		}
	}()

	return listener, password, &connCount, doneCh
}
