package sshclient

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	xssh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// startTestAgent serves an in-memory SSH agent holding one ed25519 key on a
// unix socket and returns the socket path plus the key's public half.
func startTestAgent(t *testing.T) (string, xssh.PublicKey) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate user key: %v", err)
	}
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
		t.Fatalf("add key to agent: %v", err)
	}
	userPub, err := xssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("convert public key: %v", err)
	}

	// Unix socket paths are length-limited (~104 bytes), so use a short temp dir
	// instead of t.TempDir(), which nests the full test name into the path.
	dir, err := os.MkdirTemp("", "nxagent")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sock := filepath.Join(dir, "agent.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen on agent socket: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() { _ = agent.ServeAgent(keyring, conn) }()
		}
	}()

	return sock, userPub
}

// TestAgentAuthHandshake authenticates a full SSH handshake using only the SSH
// agent. This is the regression test for the bug where the agent socket was
// closed immediately after building the auth method: the signers then failed
// with "use of closed network connection" during the handshake, so agent_ready
// servers could never authenticate.
func TestAgentAuthHandshake(t *testing.T) {
	sock, userPub := startTestAgent(t)
	t.Setenv("SSH_AUTH_SOCK", sock)

	// No password / key in the request → authMethods falls through to the agent.
	methods, cleanup, err := authMethods(ConnectionRequest{})
	if err != nil {
		t.Fatalf("authMethods: %v", err)
	}
	defer cleanup()
	if len(methods) != 1 {
		t.Fatalf("expected 1 agent auth method, got %d", len(methods))
	}

	// Minimal in-memory SSH server that accepts exactly the agent's key.
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := xssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatalf("host signer: %v", err)
	}
	serverConf := &xssh.ServerConfig{
		PublicKeyCallback: func(_ xssh.ConnMetadata, key xssh.PublicKey) (*xssh.Permissions, error) {
			if key.Type() == userPub.Type() && string(key.Marshal()) == string(userPub.Marshal()) {
				return &xssh.Permissions{}, nil
			}
			return nil, errors.New("unknown public key")
		},
	}
	serverConf.AddHostKey(hostSigner)

	// A real loopback TCP pair: the SSH version exchange writes before reading
	// on both sides, which deadlocks on an unbuffered net.Pipe.
	clientConn, serverConn := tcpPipe(t)
	serverDone := make(chan error, 1)
	go func() {
		conn, chans, reqs, err := xssh.NewServerConn(serverConn, serverConf)
		if err != nil {
			serverDone <- err
			return
		}
		go xssh.DiscardRequests(reqs)
		go func() {
			for ch := range chans {
				_ = ch.Reject(xssh.Prohibited, "test server")
			}
		}()
		serverDone <- nil
		_ = conn.Close()
	}()

	clientConf := &xssh.ClientConfig{
		User:            "nodexia",
		Auth:            methods,
		HostKeyCallback: xssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	conn, chans, reqs, err := xssh.NewClientConn(clientConn, "pipe", clientConf)
	if err != nil {
		t.Fatalf("client handshake with agent auth failed: %v", err)
	}
	client := xssh.NewClient(conn, chans, reqs)
	defer client.Close()

	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server handshake failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server handshake timed out")
	}
}

// tcpPipe returns two ends of a loopback TCP connection.
func tcpPipe(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	type accepted struct {
		conn net.Conn
		err  error
	}
	acceptCh := make(chan accepted, 1)
	go func() {
		conn, err := listener.Accept()
		acceptCh <- accepted{conn: conn, err: err}
	}()

	clientConn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	server := <-acceptCh
	if server.err != nil {
		_ = clientConn.Close()
		t.Fatalf("accept: %v", server.err)
	}
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = server.conn.Close()
	})
	return clientConn, server.conn
}

// TestAgentAuthMissingSocket keeps the no-agent failure mode intact: with no
// SSH_AUTH_SOCK and no credentials, authMethods must reject the request.
func TestAgentAuthMissingSocket(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")

	_, cleanup, err := authMethods(ConnectionRequest{})
	if err == nil {
		t.Fatal("expected an error when no credentials and no agent are available")
	}
	if cleanup == nil {
		t.Fatal("cleanup must never be nil")
	}
	cleanup()
	if want := "sshclient: runtime credentials are required"; err.Error() != want {
		t.Fatalf("unexpected error: got %q, want %q", err.Error(), want)
	}
}
