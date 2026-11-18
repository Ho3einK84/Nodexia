package sshclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	xssh "golang.org/x/crypto/ssh"
)

type commandPTYObservation struct {
	term        string
	echo        uint32
	echoSet     bool
	command     string
	stdin       string
	ptyBefore   bool
	serverError error
}

// TestStreamCommandPTYDeliversInputWithEchoDisabled verifies the SSH-layer
// contract used by the Rebecca installer: a PTY is requested before exec,
// terminal echo is disabled, and all managed stdin reaches the remote session.
func TestStreamCommandPTYDeliversInputWithEchoDisabled(t *testing.T) {
	listener, password, observations := startCommandPTYServer(t)
	address := listener.Addr().(*net.TCPAddr)

	service := &Service{
		connectTimeout: 2 * time.Second,
		commandTimeout: 2 * time.Second,
		hostKeyPolicy:  "insecure",
	}
	const input = "certificate\nprivate-key\n62050\n62051\n"
	result, err := service.StreamCommand(context.Background(), CommandRequest{
		ConnectionRequest: ConnectionRequest{
			Host:     address.IP.String(),
			Port:     address.Port,
			Username: "nodexia",
			AuthMode: "password",
			Password: password,
		},
		Command:        "run-installer",
		CommandTimeout: 2 * time.Second,
		Stdin:          strings.NewReader(input),
		AllocatePTY:    true,
	}, StreamHandlers{})
	if err != nil {
		t.Fatalf("StreamCommand: %v", err)
	}
	if result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("exit code = %v, want 0", result.ExitCode)
	}
	if strings.Contains(result.Stdout+result.Stderr, "private-key") {
		t.Fatalf("managed stdin was reflected into captured output")
	}

	select {
	case observation := <-observations:
		if observation.serverError != nil {
			t.Fatalf("test SSH server: %v", observation.serverError)
		}
		if !observation.ptyBefore {
			t.Errorf("exec arrived before a successful PTY request")
		}
		if observation.term != "xterm-256color" {
			t.Errorf("terminal = %q, want xterm-256color", observation.term)
		}
		if !observation.echoSet || observation.echo != 0 {
			t.Errorf("PTY ECHO mode = (%d, %v), want (0, true)", observation.echo, observation.echoSet)
		}
		if observation.command != "run-installer" {
			t.Errorf("command = %q, want run-installer", observation.command)
		}
		if observation.stdin != input {
			t.Errorf("stdin mismatch:\ngot  %q\nwant %q", observation.stdin, input)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for test SSH server observation")
	}
}

func startCommandPTYServer(t *testing.T) (net.Listener, string, <-chan commandPTYObservation) {
	t.Helper()

	_, hostPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate SSH host key: %v", err)
	}
	hostSigner, err := xssh.NewSignerFromKey(hostPrivate)
	if err != nil {
		t.Fatalf("create SSH host signer: %v", err)
	}

	const password = "pty-test-password"
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

	observations := make(chan commandPTYObservation, 1)
	go func() {
		observation := commandPTYObservation{}
		defer func() { observations <- observation }()

		rawConn, acceptErr := listener.Accept()
		if acceptErr != nil {
			observation.serverError = fmt.Errorf("accept: %w", acceptErr)
			return
		}
		defer func() { _ = rawConn.Close() }()

		serverConn, channels, globalRequests, handshakeErr := xssh.NewServerConn(rawConn, serverConfig)
		if handshakeErr != nil {
			observation.serverError = fmt.Errorf("handshake: %w", handshakeErr)
			return
		}
		defer func() { _ = serverConn.Close() }()
		go xssh.DiscardRequests(globalRequests)

		newChannel, ok := <-channels
		if !ok {
			observation.serverError = errors.New("client opened no channel")
			return
		}
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(xssh.UnknownChannelType, "session required")
			observation.serverError = fmt.Errorf("unexpected channel %q", newChannel.ChannelType())
			return
		}
		channel, requests, channelErr := newChannel.Accept()
		if channelErr != nil {
			observation.serverError = fmt.Errorf("accept channel: %w", channelErr)
			return
		}
		defer func() { _ = channel.Close() }()

		ptyAccepted := false
		for request := range requests {
			switch request.Type {
			case "pty-req":
				var payload struct {
					Term                    string
					Columns, Rows           uint32
					PixelWidth, PixelHeight uint32
					Modes                   string
				}
				if err := xssh.Unmarshal(request.Payload, &payload); err != nil {
					_ = request.Reply(false, nil)
					observation.serverError = fmt.Errorf("decode pty request: %w", err)
					return
				}
				observation.term = payload.Term
				observation.echo, observation.echoSet = terminalMode(payload.Modes, xssh.ECHO)
				ptyAccepted = true
				_ = request.Reply(true, nil)
			case "exec":
				var payload struct {
					Command string
				}
				if err := xssh.Unmarshal(request.Payload, &payload); err != nil {
					_ = request.Reply(false, nil)
					observation.serverError = fmt.Errorf("decode exec request: %w", err)
					return
				}
				observation.ptyBefore = ptyAccepted
				observation.command = payload.Command
				_ = request.Reply(true, nil)

				input, err := io.ReadAll(channel)
				if err != nil {
					observation.serverError = fmt.Errorf("read command stdin: %w", err)
					return
				}
				observation.stdin = string(input)
				_, _ = io.WriteString(channel, "installer accepted managed input\n")
				_, _ = channel.SendRequest("exit-status", false, xssh.Marshal(struct {
					Status uint32
				}{Status: 0}))
				return
			default:
				_ = request.Reply(false, nil)
			}
		}
		observation.serverError = errors.New("session ended without exec request")
	}()

	return listener, password, observations
}

func terminalMode(encoded string, target uint8) (uint32, bool) {
	data := []byte(encoded)
	for len(data) > 0 {
		opcode := data[0]
		data = data[1:]
		if opcode == 0 { // RFC 4254 TTY_OP_END
			return 0, false
		}
		if len(data) < 4 {
			return 0, false
		}
		value := binary.BigEndian.Uint32(data[:4])
		if opcode == target {
			return value, true
		}
		data = data[4:]
	}
	return 0, false
}
