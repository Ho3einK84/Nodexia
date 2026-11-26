package sshclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	xssh "golang.org/x/crypto/ssh"
)

func startCountingExecSSHServer(t *testing.T, password string) (string, int, *atomic.Int32) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	hostSigner, err := xssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("NewSignerFromKey: %v", err)
	}

	sshCfg := &xssh.ServerConfig{
		PasswordCallback: func(_ xssh.ConnMetadata, pass []byte) (*xssh.Permissions, error) {
			if string(pass) == password {
				return nil, nil
			}
			return nil, os.ErrPermission
		},
	}
	sshCfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	var handshakes atomic.Int32
	go func() {
		for {
			rawConn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, chans, reqs, err := xssh.NewServerConn(c, sshCfg)
				if err != nil {
					return
				}
				handshakes.Add(1)
				go func() {
					for req := range reqs {
						if req.WantReply {
							_ = req.Reply(true, nil)
						}
					}
				}()
				for newChan := range chans {
					if newChan.ChannelType() != "session" {
						_ = newChan.Reject(xssh.UnknownChannelType, "unsupported")
						continue
					}
					ch, requests, err := newChan.Accept()
					if err != nil {
						return
					}
					go func(in <-chan *xssh.Request, channel xssh.Channel) {
						defer func() { _ = channel.Close() }()
						for req := range in {
							if req.Type == "exec" {
								if req.WantReply {
									_ = req.Reply(true, nil)
								}
								_, _ = channel.Write([]byte("ok\n"))
								_, _ = channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
								return
							}
							if req.WantReply {
								_ = req.Reply(false, nil)
							}
						}
					}(requests, ch)
				}
			}(rawConn)
		}
	}()

	tcpAddr := ln.Addr().(*net.TCPAddr)
	return tcpAddr.IP.String(), tcpAddr.Port, &handshakes
}

func TestPoolReusesConnectionAndEvictsHost(t *testing.T) {
	const pass = "pool-secret"
	host, port, handshakes := startCountingExecSSHServer(t, pass)

	svc, knownHostsPath := newTestService(t)
	t.Cleanup(func() { _ = svc.Close() })

	req := CommandRequest{
		ConnectionRequest: ConnectionRequest{
			Host:           host,
			Port:           port,
			Username:       "root",
			AuthMode:       "password",
			Password:       pass,
			ConnectTimeout: 5 * time.Second,
		},
		Command:        "echo ok",
		CommandTimeout: 5 * time.Second,
	}

	// First command dials and pins the TOFU host key on disk.
	res, err := svc.RunCommand(context.Background(), req)
	if err != nil {
		t.Fatalf("first RunCommand failed: %v", err)
	}
	if res.Stdout != "ok\n" {
		t.Fatalf("first RunCommand stdout = %q, want ok\\n", res.Stdout)
	}
	if got := handshakes.Load(); got != 1 {
		t.Fatalf("handshakes after 1st command = %d, want 1", got)
	}

	info1, err := os.Stat(knownHostsPath)
	if err != nil {
		t.Fatalf("known_hosts.json not created: %v", err)
	}

	// Subsequent commands reuse the pooled SSH connection with zero extra handshakes.
	for i := 0; i < 3; i++ {
		if _, err := svc.RunCommand(context.Background(), req); err != nil {
			t.Fatalf("pooled RunCommand #%d failed: %v", i+2, err)
		}
	}
	if got := handshakes.Load(); got != 1 {
		t.Fatalf("handshakes after pooled commands = %d, want 1", got)
	}

	// Evicting the host forces a fresh handshake, while the cached known_hosts
	// entry verifies in memory without rewriting known_hosts.json.
	svc.EvictHost(host, port)
	if _, err := svc.RunCommand(context.Background(), req); err != nil {
		t.Fatalf("post-evict RunCommand failed: %v", err)
	}
	if got := handshakes.Load(); got != 2 {
		t.Fatalf("handshakes after EvictHost = %d, want 2", got)
	}
	info2, err := os.Stat(knownHostsPath)
	if err != nil {
		t.Fatalf("stat known_hosts.json: %v", err)
	}
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatalf("known_hosts.json was rewritten on steady-state matching host key")
	}
}
