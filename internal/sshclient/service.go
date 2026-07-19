package sshclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/pkg/sftp"
	xssh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type Service struct {
	connectTimeout time.Duration
	commandTimeout time.Duration
	hostKeyPolicy  string
	knownHostsPath string
	// hostKeysMu guards all reads and writes of the known-hosts file.
	// IMPORTANT: Service methods use *Service (pointer) receivers so that this
	// mutex is shared across all callers rather than copied per call.
	hostKeysMu sync.Mutex
}

type ConnectionRequest struct {
	Host           string
	Port           int
	Username       string
	AuthMode       string
	Password       string
	PrivateKeyPEM  string
	KeyPassphrase  string
	ConnectTimeout time.Duration
}

type ConnectionResult struct {
	RemoteAddress string
	Duration      time.Duration
}

type CommandRequest struct {
	ConnectionRequest
	Command        string
	CommandTimeout time.Duration
}

type CommandResult struct {
	Stdout      string
	Stderr      string
	ExitCode    *int
	Duration    time.Duration
	CompletedAt time.Time
}

type StreamHandlers struct {
	OnStdout func(string)
	OnStderr func(string)
}

type FileEntry struct {
	Name       string
	Path       string
	Size       int64
	Mode       string
	IsDir      bool
	ModifiedAt time.Time
}

type DirectoryListing struct {
	Path    string
	Entries []FileEntry
}

type RemoteFile struct {
	Name       string
	Path       string
	Size       int64
	Mode       string
	ModifiedAt time.Time
	Content    io.ReadCloser
}

func New(cfg config.SSHConfig, security config.SecurityConfig) *Service {
	return &Service{
		connectTimeout: cfg.ConnectTimeout,
		commandTimeout: cfg.CommandTimeout,
		hostKeyPolicy:  strings.TrimSpace(security.SSHHostKeyPolicy),
		knownHostsPath: strings.TrimSpace(security.SSHKnownHostsPath),
	}
}

// All methods below use *Service (pointer receivers) so that hostKeysMu is
// shared across callers rather than silently copied per call site.

func (s *Service) TestConnection(ctx context.Context, req ConnectionRequest) (ConnectionResult, error) {
	startedAt := time.Now()
	client, address, err := s.connect(ctx, req)
	if err != nil {
		return ConnectionResult{}, err
	}
	defer client.Close()

	return ConnectionResult{
		RemoteAddress: address,
		Duration:      time.Since(startedAt),
	}, nil
}

func (s *Service) RunCommand(ctx context.Context, req CommandRequest) (CommandResult, error) {
	return s.StreamCommand(ctx, req, StreamHandlers{})
}

func (s *Service) StreamCommand(ctx context.Context, req CommandRequest, handlers StreamHandlers) (CommandResult, error) {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return CommandResult{}, errors.New("sshclient: command cannot be empty")
	}

	startedAt := time.Now()
	client, _, err := s.connect(ctx, req.ConnectionRequest)
	if err != nil {
		return CommandResult{}, err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return CommandResult{}, fmt.Errorf("sshclient: create session: %w", err)
	}
	defer session.Close()

	stdoutReader, err := session.StdoutPipe()
	if err != nil {
		return CommandResult{}, fmt.Errorf("sshclient: stdout pipe: %w", err)
	}
	stderrReader, err := session.StderrPipe()
	if err != nil {
		return CommandResult{}, fmt.Errorf("sshclient: stderr pipe: %w", err)
	}

	if err := session.Start(command); err != nil {
		return CommandResult{}, fmt.Errorf("sshclient: start command: %w", err)
	}

	stdout := newLimitedBuffer()
	stderr := newLimitedBuffer()
	var streamWG sync.WaitGroup
	streamWG.Add(2)
	go streamOutput(stdoutReader, stdout, handlers.OnStdout, &streamWG)
	go streamOutput(stderrReader, stderr, handlers.OnStderr, &streamWG)

	waitTimeout := s.resolveCommandTimeout(req.CommandTimeout)
	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()

	waitResult := make(chan error, 1)
	go func() {
		waitResult <- session.Wait()
	}()

	result := CommandResult{
		CompletedAt: time.Now().UTC(),
	}

	select {
	case err := <-waitResult:
		streamWG.Wait()
		result.Stdout = stdout.String()
		result.Stderr = stderr.String()
		result.Duration = time.Since(startedAt)
		result.CompletedAt = time.Now().UTC()

		if err == nil {
			exitCode := 0
			result.ExitCode = &exitCode
			return result, nil
		}

		var exitErr *xssh.ExitError
		if errors.As(err, &exitErr) {
			exitCode := exitErr.ExitStatus()
			result.ExitCode = &exitCode
			return result, nil
		}

		return result, fmt.Errorf("sshclient: wait for command: %w", err)
	case <-waitCtx.Done():
		result.Duration = time.Since(startedAt)
		result.CompletedAt = time.Now().UTC()
		_ = session.Close()
		streamWG.Wait()
		result.Stdout = stdout.String()
		result.Stderr = stderr.String()
		return result, fmt.Errorf("sshclient: command timed out after %s", waitTimeout)
	}
}

// maxStreamLineBytes bounds a single line read by StreamScan so a hostile or
// broken remote cannot drive unbounded memory growth on one token.
const maxStreamLineBytes = 256 * 1024

// StreamScan opens a connection, runs command, and invokes onLine for every
// newline-delimited stdout line until the command exits or ctx is cancelled.
//
// Unlike RunCommand/StreamCommand it applies NO command timeout — the caller
// bounds the session lifetime via ctx — and it does not accumulate output in
// memory. It is built for long-lived streaming probes (the live-metrics
// collector loop). Stderr is drained and discarded so the remote pipe never
// blocks. When ctx is cancelled the session and client are closed, which tears
// down the remote loop and unblocks the scanner.
func (s *Service) StreamScan(ctx context.Context, req CommandRequest, onLine func(string)) error {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return errors.New("sshclient: command cannot be empty")
	}

	client, _, err := s.connect(ctx, req.ConnectionRequest)
	if err != nil {
		return err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("sshclient: create session: %w", err)
	}
	defer session.Close()

	stdoutReader, err := session.StdoutPipe()
	if err != nil {
		return fmt.Errorf("sshclient: stdout pipe: %w", err)
	}
	stderrReader, err := session.StderrPipe()
	if err != nil {
		return fmt.Errorf("sshclient: stderr pipe: %w", err)
	}

	if err := session.Start(command); err != nil {
		return fmt.Errorf("sshclient: start command: %w", err)
	}

	// Closing the session/client on ctx cancellation tears down the remote loop
	// and unblocks the scanner below.
	stop := context.AfterFunc(ctx, func() {
		_ = session.Close()
		_ = client.Close()
	})
	defer stop()

	go func() { _, _ = io.Copy(io.Discard, stderrReader) }()

	scanner := bufio.NewScanner(stdoutReader)
	scanner.Buffer(make([]byte, 0, 64*1024), maxStreamLineBytes)
	for scanner.Scan() {
		if onLine != nil {
			onLine(scanner.Text())
		}
	}

	waitErr := session.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return fmt.Errorf("sshclient: scan stream: %w", scanErr)
	}
	if waitErr != nil {
		var exitErr *xssh.ExitError
		if errors.As(waitErr, &exitErr) {
			return fmt.Errorf("sshclient: remote stream exited with status %d", exitErr.ExitStatus())
		}
		return fmt.Errorf("sshclient: stream wait: %w", waitErr)
	}
	return nil
}

// ResizeRequest carries a PTY window-change request.
type ResizeRequest struct {
	Rows uint32
	Cols uint32
}

// InteractiveIO wires the I/O streams and resize channel for an OpenShell
// call.  Resize must be a non-nil, readable channel; callers should close it
// or cancel the context when they are done with the session.
type InteractiveIO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Rows   uint32
	Cols   uint32
	Resize <-chan ResizeRequest
}

// OpenShell dials the server, opens a PTY-backed interactive shell, and blocks
// until the remote shell exits or ctx is cancelled.
//
// Connect timeout applies only to the initial dial; the shell session is
// long-lived.  Command timeouts are intentionally NOT applied here — use
// ctx cancellation to bound the session lifetime.
func (s *Service) OpenShell(ctx context.Context, req ConnectionRequest, pio InteractiveIO) error {
	client, _, err := s.connect(ctx, req)
	if err != nil {
		return err
	}

	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return fmt.Errorf("sshclient: create shell session: %w", err)
	}

	modes := xssh.TerminalModes{
		xssh.ECHO:          1,
		xssh.TTY_OP_ISPEED: 38400,
		xssh.TTY_OP_OSPEED: 38400,
	}
	rows, cols := pio.Rows, pio.Cols
	if rows == 0 {
		rows = 24
	}
	if cols == 0 {
		cols = 80
	}
	if err := session.RequestPty("xterm-256color", int(rows), int(cols), modes); err != nil {
		session.Close()
		client.Close()
		return fmt.Errorf("sshclient: request pty: %w", err)
	}

	session.Stdin = pio.Stdin
	session.Stdout = pio.Stdout
	session.Stderr = pio.Stderr

	if err := session.Shell(); err != nil {
		session.Close()
		client.Close()
		return fmt.Errorf("sshclient: start shell: %w", err)
	}

	// Forward resize requests until ctx ends.
	if pio.Resize != nil {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case r, ok := <-pio.Resize:
					if !ok {
						return
					}
					_ = session.WindowChange(int(r.Rows), int(r.Cols))
				}
			}
		}()
	}

	waitDone := make(chan error, 1)
	go func() { waitDone <- session.Wait() }()

	select {
	case <-ctx.Done():
		_ = session.Close()
		_ = client.Close()
		<-waitDone
		return ctx.Err()
	case err := <-waitDone:
		_ = client.Close()
		return err
	}
}

func (s *Service) ListDirectory(ctx context.Context, req ConnectionRequest, remotePath string) (DirectoryListing, error) {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return DirectoryListing{}, err
	}
	defer cleanup()

	entries, err := sftpClient.ReadDir(remotePath)
	if err != nil {
		return DirectoryListing{}, fmt.Errorf("sshclient: read directory %s: %w", remotePath, err)
	}

	items := make([]FileEntry, 0, len(entries))
	for _, entry := range entries {
		items = append(items, newFileEntry(remotePath, entry))
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].IsDir != items[j].IsDir {
			return items[i].IsDir
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})

	return DirectoryListing{
		Path:    remotePath,
		Entries: items,
	}, nil
}

func (s *Service) OpenFile(ctx context.Context, req ConnectionRequest, remotePath string) (RemoteFile, error) {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return RemoteFile{}, err
	}

	info, err := sftpClient.Stat(remotePath)
	if err != nil {
		cleanup()
		return RemoteFile{}, fmt.Errorf("sshclient: stat file %s: %w", remotePath, err)
	}
	if info.IsDir() {
		cleanup()
		return RemoteFile{}, fmt.Errorf("sshclient: %s is a directory", remotePath)
	}

	file, err := sftpClient.Open(remotePath)
	if err != nil {
		cleanup()
		return RemoteFile{}, fmt.Errorf("sshclient: open file %s: %w", remotePath, err)
	}

	return RemoteFile{
		Name:       info.Name(),
		Path:       remotePath,
		Size:       info.Size(),
		Mode:       info.Mode().String(),
		ModifiedAt: info.ModTime().UTC(),
		Content: &remoteReadCloser{
			reader:  file,
			cleanup: cleanup,
			closers: nil,
		},
	}, nil
}

// UploadFile streams content into remotePath over SFTP, creating or truncating
// the destination. It refuses to overwrite an existing directory and returns
// the number of bytes written. The copy is bounded by ctx cancellation via the
// caller-supplied reader (e.g. an aborted HTTP request body unblocks the read).
func (s *Service) UploadFile(ctx context.Context, req ConnectionRequest, remotePath string, content io.Reader) (int64, error) {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return 0, err
	}
	defer cleanup()

	if info, statErr := sftpClient.Stat(remotePath); statErr == nil && info.IsDir() {
		return 0, fmt.Errorf("sshclient: %s is a directory", remotePath)
	}

	remoteFile, err := sftpClient.Create(remotePath)
	if err != nil {
		return 0, fmt.Errorf("sshclient: create remote file %s: %w", remotePath, err)
	}

	written, copyErr := io.Copy(remoteFile, content)
	closeErr := remoteFile.Close()
	if copyErr != nil {
		return written, fmt.Errorf("sshclient: write remote file %s: %w", remotePath, copyErr)
	}
	if closeErr != nil {
		return written, fmt.Errorf("sshclient: finalize remote file %s: %w", remotePath, closeErr)
	}
	return written, nil
}

// RemovePath deletes a file or directory over SFTP. Directories are removed
// recursively when recursive is true, otherwise only empty directories succeed.
func (s *Service) RemovePath(ctx context.Context, req ConnectionRequest, remotePath string, recursive bool) error {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return err
	}
	defer cleanup()

	info, err := sftpClient.Stat(remotePath)
	if err != nil {
		return fmt.Errorf("sshclient: stat %s: %w", remotePath, err)
	}

	switch {
	case info.IsDir() && recursive:
		err = sftpClient.RemoveAll(remotePath)
	case info.IsDir():
		err = sftpClient.RemoveDirectory(remotePath)
	default:
		err = sftpClient.Remove(remotePath)
	}
	if err != nil {
		return fmt.Errorf("sshclient: remove %s: %w", remotePath, err)
	}
	return nil
}

// RenamePath renames or moves oldPath to newPath over SFTP. It refuses to
// clobber an existing destination so renames never silently overwrite data.
func (s *Service) RenamePath(ctx context.Context, req ConnectionRequest, oldPath, newPath string) error {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return err
	}
	defer cleanup()

	if _, err := sftpClient.Stat(newPath); err == nil {
		return fmt.Errorf("sshclient: %s already exists", newPath)
	}
	if err := sftpClient.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("sshclient: rename %s to %s: %w", oldPath, newPath, err)
	}
	return nil
}

// MakeDirectory creates a single directory over SFTP. It refuses to act when a
// file or directory already exists at the target.
func (s *Service) MakeDirectory(ctx context.Context, req ConnectionRequest, remotePath string) error {
	_, sftpClient, cleanup, err := s.openSFTP(ctx, req)
	if err != nil {
		return err
	}
	defer cleanup()

	if _, err := sftpClient.Stat(remotePath); err == nil {
		return fmt.Errorf("sshclient: %s already exists", remotePath)
	}
	if err := sftpClient.Mkdir(remotePath); err != nil {
		return fmt.Errorf("sshclient: create directory %s: %w", remotePath, err)
	}
	return nil
}

// Algorithm preferences pinned to a modern, OpenSSH-compatible set. We set
// these explicitly instead of relying on x/crypto/ssh defaults: the defaults
// lead with mlkem768x25519-sha256, and offering a post-quantum key exchange
// first has been observed to stall handshakes against some hardened/stock sshd
// builds (x/crypto's own interop tests strip it before recording). Leading with
// curve25519 keeps negotiation deterministic and interoperable with stock
// OpenSSH 9.x while still covering ECDH and the standard DH groups.
var (
	kexAlgorithms = []string{
		xssh.KeyExchangeCurve25519,
		xssh.KeyExchangeECDHP256,
		xssh.KeyExchangeECDHP384,
		xssh.KeyExchangeECDHP521,
		xssh.KeyExchangeDH14SHA256,
		xssh.KeyExchangeDH16SHA512,
		xssh.KeyExchangeDHGEXSHA256,
	}
	ciphers = []string{
		xssh.CipherChaCha20Poly1305,
		xssh.CipherAES128GCM,
		xssh.CipherAES256GCM,
		xssh.CipherAES128CTR,
		xssh.CipherAES192CTR,
		xssh.CipherAES256CTR,
	}
	macs = []string{
		xssh.HMACSHA256ETM,
		xssh.HMACSHA512ETM,
		xssh.HMACSHA256,
		xssh.HMACSHA512,
		// HMAC-SHA1 is a last-resort compatibility fallback for legacy SSH
		// peers; prefer the SHA-256/512 options above when available.
		xssh.HMACSHA1,
	}
	hostKeyAlgorithms = []string{
		xssh.CertAlgoED25519v01,
		xssh.CertAlgoECDSA256v01,
		xssh.CertAlgoECDSA384v01,
		xssh.CertAlgoECDSA521v01,
		xssh.CertAlgoRSASHA256v01,
		xssh.CertAlgoRSASHA512v01,
		xssh.KeyAlgoED25519,
		xssh.KeyAlgoECDSA256,
		xssh.KeyAlgoECDSA384,
		xssh.KeyAlgoECDSA521,
		xssh.KeyAlgoRSASHA256,
		xssh.KeyAlgoRSASHA512,
		xssh.KeyAlgoRSA,
	}
)

func (s *Service) connect(ctx context.Context, req ConnectionRequest) (*xssh.Client, string, error) {
	host := strings.TrimSpace(req.Host)
	username := strings.TrimSpace(req.Username)
	if host == "" {
		return nil, "", errors.New("sshclient: host cannot be empty")
	}
	if username == "" {
		return nil, "", errors.New("sshclient: username cannot be empty")
	}

	port := req.Port
	if port <= 0 {
		port = 22
	}

	authMethods, closeAuth, err := authMethods(req)
	if err != nil {
		return nil, "", err
	}
	// Auth resources (the SSH-agent socket) are only needed while the handshake
	// below authenticates; connect returns once it completes, so a deferred close
	// releases them at exactly the right time.
	defer closeAuth()

	address := net.JoinHostPort(host, strconv.Itoa(port))
	dialCtx, cancel := context.WithTimeout(ctx, s.resolveConnectTimeout(req.ConnectTimeout))
	defer cancel()

	slog.Debug("sshclient: dialing", slog.String("address", address), slog.String("user", username))

	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", address)
	if err != nil {
		return nil, "", fmt.Errorf("sshclient: dial %s: %w", address, err)
	}

	// xssh.NewClientConn does not honour dialCtx, so a stalled SSH handshake
	// would block far beyond the connect timeout (observed ~24s hangs against
	// hardened sshd). Bound the handshake with a connection deadline and abort
	// it promptly if the caller's context is cancelled.
	if deadline, ok := dialCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stopAbort := context.AfterFunc(dialCtx, func() { _ = conn.SetDeadline(time.Now()) })

	slog.Debug("sshclient: starting handshake", slog.String("address", address))

	clientConn, channels, requests, err := xssh.NewClientConn(conn, address, &xssh.ClientConfig{
		User:              username,
		Auth:              authMethods,
		HostKeyCallback:   s.hostKeyCallback(address),
		HostKeyAlgorithms: hostKeyAlgorithms,
		Config: xssh.Config{
			KeyExchanges: kexAlgorithms,
			Ciphers:      ciphers,
			MACs:         macs,
		},
	})
	stopAbort()
	if err != nil {
		_ = conn.Close()
		slog.Warn("sshclient: handshake failed",
			slog.String("address", address),
			slog.String("error", err.Error()),
		)
		return nil, "", fmt.Errorf("sshclient: handshake %s: %w", address, err)
	}

	// Clear the handshake deadline so the established session is not torn down
	// by the connect timeout; commands enforce their own timeouts.
	_ = conn.SetDeadline(time.Time{})

	slog.Debug("sshclient: handshake complete", slog.String("address", address))

	return xssh.NewClient(clientConn, channels, requests), address, nil
}

func (s *Service) hostKeyCallback(address string) xssh.HostKeyCallback {
	if strings.EqualFold(strings.TrimSpace(s.hostKeyPolicy), "insecure") {
		slog.Warn("sshclient: host key verification disabled (insecure mode)", slog.String("address", address))
		return xssh.InsecureIgnoreHostKey()
	}

	return func(_ string, _ net.Addr, key xssh.PublicKey) error {
		return s.verifyOrTrustHostKey(address, key)
	}
}

func (s *Service) verifyOrTrustHostKey(address string, key xssh.PublicKey) error {
	s.hostKeysMu.Lock()
	defer s.hostKeysMu.Unlock()

	store, err := s.loadKnownHosts()
	if err != nil {
		return err
	}

	serializedKey := marshalAuthorizedKey(key)
	fingerprint := xssh.FingerprintSHA256(key)
	entry, exists := store[address]
	if exists {
		if entry.AuthorizedKey == serializedKey {
			slog.Debug("sshclient: host key verified",
				slog.String("address", address),
				slog.String("fingerprint", fingerprint),
			)
			entry.LastSeenAt = time.Now().UTC()
			store[address] = entry
			return s.saveKnownHosts(store)
		}
		slog.Warn("sshclient: host key mismatch — possible MITM or key rotation",
			slog.String("address", address),
			slog.String("expected_fingerprint", entry.Fingerprint),
			slog.String("observed_fingerprint", fingerprint),
		)
		return fmt.Errorf("sshclient: host key mismatch for %s: expected %s, got %s", address, entry.Fingerprint, fingerprint)
	}

	slog.Info("sshclient: trusting new host key (TOFU)",
		slog.String("address", address),
		slog.String("fingerprint", fingerprint),
		slog.String("key_type", key.Type()),
	)
	store[address] = knownHostEntry{
		AuthorizedKey: serializedKey,
		Fingerprint:   fingerprint,
		TrustedAt:     time.Now().UTC(),
		LastSeenAt:    time.Now().UTC(),
	}
	return s.saveKnownHosts(store)
}

func (s *Service) ForgetHostKey(address string) error {
	s.hostKeysMu.Lock()
	defer s.hostKeysMu.Unlock()

	store, err := s.loadKnownHosts()
	if err != nil {
		return err
	}

	if _, exists := store[address]; !exists {
		return nil
	}

	delete(store, address)
	slog.Info("sshclient: host key forgotten", slog.String("address", address))
	return s.saveKnownHosts(store)
}

func (s *Service) openSFTP(ctx context.Context, req ConnectionRequest) (*xssh.Client, *sftp.Client, func(), error) {
	client, _, err := s.connect(ctx, req)
	if err != nil {
		return nil, nil, func() {}, err
	}

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		client.Close()
		return nil, nil, func() {}, fmt.Errorf("sshclient: create sftp client: %w", err)
	}

	return client, sftpClient, func() {
		_ = sftpClient.Close()
		_ = client.Close()
	}, nil
}

// authMethods resolves the auth methods for a connection request. The returned
// cleanup func releases any resource the methods hold open (today: the SSH-agent
// socket) and must be called only after authentication has completed — the
// agent-backed signers keep using the socket for every signature during the
// handshake. It is never nil.
func authMethods(req ConnectionRequest) ([]xssh.AuthMethod, func(), error) {
	password := strings.TrimSpace(req.Password)
	privateKey := strings.TrimSpace(req.PrivateKeyPEM)
	authMode := strings.TrimSpace(req.AuthMode)
	noCleanup := func() {}

	switch authMode {
	case "password":
		if password == "" {
			return nil, noCleanup, errors.New("sshclient: password is required for password auth mode")
		}
		return []xssh.AuthMethod{xssh.Password(req.Password)}, noCleanup, nil
	case "key":
		signer, err := signerFromRequest(req)
		if err != nil {
			return nil, noCleanup, err
		}
		return []xssh.AuthMethod{xssh.PublicKeys(signer)}, noCleanup, nil
	case "hybrid":
		methods := make([]xssh.AuthMethod, 0, 2)
		if privateKey != "" {
			signer, err := signerFromRequest(req)
			if err != nil {
				return nil, noCleanup, err
			}
			methods = append(methods, xssh.PublicKeys(signer))
		}
		if password != "" {
			methods = append(methods, xssh.Password(req.Password))
		}
		if len(methods) == 0 {
			return nil, noCleanup, errors.New("sshclient: password or private key is required for hybrid auth mode")
		}
		return methods, noCleanup, nil
	default:
		if privateKey != "" {
			signer, err := signerFromRequest(req)
			if err != nil {
				return nil, noCleanup, err
			}
			return []xssh.AuthMethod{xssh.PublicKeys(signer)}, noCleanup, nil
		}
		if password != "" {
			return []xssh.AuthMethod{xssh.Password(req.Password)}, noCleanup, nil
		}

		agentMethods, agentCleanup, agentErr := sshAgentAuth()
		if agentErr == nil && len(agentMethods) > 0 {
			return agentMethods, agentCleanup, nil
		}

		return nil, noCleanup, errors.New("sshclient: runtime credentials are required")
	}
}

// sshAgentAuth connects to the local SSH agent and returns an auth method whose
// signers proxy signature requests over that connection. The socket must stay
// open for the whole handshake (each publickey attempt round-trips through the
// agent), so the caller closes it via the returned cleanup once authentication
// is done — closing it up front would make every agent-backed handshake fail
// with "use of closed network connection".
func sshAgentAuth() ([]xssh.AuthMethod, func(), error) {
	authSock := os.Getenv("SSH_AUTH_SOCK")
	if authSock == "" {
		return nil, nil, errors.New("sshclient: SSH_AUTH_SOCK not set")
	}

	conn, err := net.Dial("unix", authSock)
	if err != nil {
		return nil, nil, fmt.Errorf("sshclient: dial SSH agent: %w", err)
	}

	agentClient := agent.NewClient(conn)
	cleanup := func() { _ = conn.Close() }
	return []xssh.AuthMethod{xssh.PublicKeysCallback(agentClient.Signers)}, cleanup, nil
}

func signerFromRequest(req ConnectionRequest) (xssh.Signer, error) {
	privateKey := strings.TrimSpace(req.PrivateKeyPEM)
	if privateKey == "" {
		return nil, errors.New("sshclient: private key is required for key auth mode")
	}

	passphrase := strings.TrimSpace(req.KeyPassphrase)
	if passphrase == "" {
		signer, err := xssh.ParsePrivateKey([]byte(req.PrivateKeyPEM))
		if err != nil {
			return nil, fmt.Errorf("sshclient: parse private key: %w", err)
		}
		return signer, nil
	}

	signer, err := xssh.ParsePrivateKeyWithPassphrase([]byte(req.PrivateKeyPEM), []byte(req.KeyPassphrase))
	if err != nil {
		return nil, fmt.Errorf("sshclient: parse private key with passphrase: %w", err)
	}

	return signer, nil
}

type knownHostEntry struct {
	AuthorizedKey string    `json:"authorized_key"`
	Fingerprint   string    `json:"fingerprint"`
	TrustedAt     time.Time `json:"trusted_at"`
	LastSeenAt    time.Time `json:"last_seen_at"`
}

// loadKnownHosts reads and parses the known-hosts JSON file.
//
// Corruption recovery: if the file exists but cannot be parsed as valid JSON
// (most likely from a previous concurrent-write race that left interleaved
// bytes), the file is renamed to a timestamped backup and an empty store is
// returned so that TOFU re-trust can proceed on the next connection.  The
// backup lets an operator inspect or restore the original keys.
//
// Must be called with s.hostKeysMu held.
func (s *Service) loadKnownHosts() (map[string]knownHostEntry, error) {
	if strings.TrimSpace(s.knownHostsPath) == "" {
		return nil, errors.New("sshclient: known hosts path is not configured")
	}

	slog.Debug("sshclient: loading known hosts", slog.String("path", s.knownHostsPath))

	content, err := os.ReadFile(s.knownHostsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Debug("sshclient: known hosts file does not exist, starting fresh",
				slog.String("path", s.knownHostsPath))
			return map[string]knownHostEntry{}, nil
		}
		return nil, fmt.Errorf("sshclient: read known hosts: %w", err)
	}

	if len(bytes.TrimSpace(content)) == 0 {
		slog.Debug("sshclient: known hosts file is empty, starting fresh",
			slog.String("path", s.knownHostsPath))
		return map[string]knownHostEntry{}, nil
	}

	store := map[string]knownHostEntry{}
	if err := json.Unmarshal(content, &store); err != nil {
		// The file is present but not valid JSON.  This is most commonly caused
		// by a concurrent-write race (see saveKnownHosts for details).  Back up
		// the corrupted file and start with an empty store so that subsequent
		// connections can re-establish TOFU trust without crashing.
		backup := s.knownHostsPath + ".corrupt." + strconv.FormatInt(time.Now().UnixNano(), 10)
		if renameErr := os.Rename(s.knownHostsPath, backup); renameErr != nil {
			slog.Error("sshclient: known hosts file is corrupted and backup failed — removing",
				slog.String("path", s.knownHostsPath),
				slog.String("backup", backup),
				slog.String("parse_error", err.Error()),
				slog.String("rename_error", renameErr.Error()),
			)
			_ = os.Remove(s.knownHostsPath)
		} else {
			slog.Warn("sshclient: known hosts file is corrupted — backed up and starting fresh",
				slog.String("path", s.knownHostsPath),
				slog.String("backup", backup),
				slog.String("parse_error", err.Error()),
			)
		}
		return map[string]knownHostEntry{}, nil
	}

	slog.Debug("sshclient: known hosts loaded", slog.String("path", s.knownHostsPath), slog.Int("entries", len(store)))
	return store, nil
}

// saveKnownHosts writes the known-hosts store to disk atomically.
//
// The store is marshalled to a sibling temp file first, then renamed into
// place.  os.Rename is atomic on Linux (same filesystem), so readers always
// see either the old or the new file — never a partially-written one.  This
// also eliminates the truncate-then-write race that the previous os.WriteFile
// approach suffered under concurrent calls.
//
// Must be called with s.hostKeysMu held.
func (s *Service) saveKnownHosts(store map[string]knownHostEntry) error {
	if strings.TrimSpace(s.knownHostsPath) == "" {
		return errors.New("sshclient: known hosts path is not configured")
	}

	dir := filepath.Dir(s.knownHostsPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("sshclient: create known hosts directory: %w", err)
	}

	payload, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("sshclient: marshal known hosts: %w", err)
	}

	// Write to a temp file in the same directory so os.Rename is atomic.
	tmp := filepath.Join(dir, "."+filepath.Base(s.knownHostsPath)+".tmp")
	if err := os.WriteFile(tmp, append(payload, '\n'), 0o600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("sshclient: write known hosts temp file: %w", err)
	}

	if err := os.Rename(tmp, s.knownHostsPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("sshclient: atomically replace known hosts file: %w", err)
	}

	slog.Debug("sshclient: known hosts saved",
		slog.String("path", s.knownHostsPath),
		slog.Int("entries", len(store)),
	)
	return nil
}

func marshalAuthorizedKey(key xssh.PublicKey) string {
	return strings.TrimSpace(string(xssh.MarshalAuthorizedKey(key)))
}

func (s *Service) resolveConnectTimeout(override time.Duration) time.Duration {
	if override > 0 {
		return override
	}
	if s.connectTimeout > 0 {
		return s.connectTimeout
	}
	return 10 * time.Second
}

func (s *Service) resolveCommandTimeout(override time.Duration) time.Duration {
	if override > 0 {
		return override
	}
	if s.commandTimeout > 0 {
		return s.commandTimeout
	}
	return 20 * time.Second
}

const maxCommandOutputBytes = 1 << 20 // 1 MiB per stdout/stderr stream

// limitedBuffer accumulates up to maxCommandOutputBytes bytes, then stops
// writing and appends a single truncation notice. This prevents unbounded
// memory growth when a command produces very large output.
type limitedBuffer struct {
	buf       bytes.Buffer
	remaining int
	capped    bool
}

func newLimitedBuffer() *limitedBuffer {
	return &limitedBuffer{remaining: maxCommandOutputBytes}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.capped {
		return len(p), nil
	}
	if len(p) > b.remaining {
		_, _ = b.buf.Write(p[:b.remaining])
		b.remaining = 0
		b.capped = true
		_, _ = b.buf.WriteString("\n[output truncated at 1 MiB]")
		return len(p), nil
	}
	n, err := b.buf.Write(p)
	b.remaining -= n
	return n, err
}

func (b *limitedBuffer) String() string { return b.buf.String() }

func streamOutput(reader io.Reader, output io.Writer, onChunk func(string), wg *sync.WaitGroup) {
	defer wg.Done()

	buffered := bufio.NewReader(reader)
	chunk := make([]byte, 1024)
	for {
		readCount, err := buffered.Read(chunk)
		if readCount > 0 {
			piece := string(chunk[:readCount])
			_, _ = io.WriteString(output, piece)
			if onChunk != nil {
				onChunk(piece)
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return
		}
		_, _ = io.WriteString(output, "\n[stream read error] "+err.Error())
		return
	}
}

func newFileEntry(parent string, info os.FileInfo) FileEntry {
	childPath := strings.TrimRight(parent, "/")
	if childPath == "" {
		childPath = "/"
	}
	if childPath == "/" {
		childPath = "/" + info.Name()
	} else {
		childPath = childPath + "/" + info.Name()
	}

	return FileEntry{
		Name:       info.Name(),
		Path:       childPath,
		Size:       info.Size(),
		Mode:       info.Mode().String(),
		IsDir:      info.IsDir(),
		ModifiedAt: info.ModTime().UTC(),
	}
}

type remoteReadCloser struct {
	reader  io.ReadCloser
	cleanup func()
	closers []io.Closer
}

func (r *remoteReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *remoteReadCloser) Close() error {
	var combined error
	if r.reader != nil {
		if err := r.reader.Close(); err != nil {
			combined = errors.Join(combined, err)
		}
		r.reader = nil
	}

	for _, closer := range r.closers {
		if closer == nil {
			continue
		}
		if err := closer.Close(); err != nil {
			combined = errors.Join(combined, err)
		}
	}

	if r.cleanup != nil {
		r.cleanup()
		r.cleanup = nil
	}

	r.closers = nil
	return combined
}
