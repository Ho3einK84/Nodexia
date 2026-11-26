package sshclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"
	xssh "golang.org/x/crypto/ssh"
)

const (
	poolIdleTTL           = 90 * time.Second
	poolReapInterval      = 30 * time.Second
	poolKeepaliveInterval = 30 * time.Second
	poolKeepaliveTimeout  = 5 * time.Second
)

type pooledConn struct {
	key        string
	address    string
	client     *xssh.Client
	sftpMu     sync.Mutex
	sftpClient *sftp.Client

	inUse    atomic.Int32
	lastUsed atomic.Int64
	lastPing atomic.Int64
	closed   atomic.Bool
}

func (p *pooledConn) release() {
	p.lastUsed.Store(time.Now().UnixNano())
	remaining := p.inUse.Add(-1)
	if remaining <= 0 && p.closed.Load() {
		p.closeNow()
	}
}

func (p *pooledConn) closeNow() {
	p.sftpMu.Lock()
	sc := p.sftpClient
	p.sftpClient = nil
	p.sftpMu.Unlock()
	if sc != nil {
		_ = sc.Close()
	}
	if p.client != nil {
		_ = p.client.Close()
	}
}

func (p *pooledConn) getOrCreateSFTP() (*sftp.Client, error) {
	p.sftpMu.Lock()
	defer p.sftpMu.Unlock()
	if p.closed.Load() {
		return nil, fmt.Errorf("sshclient: pooled connection closed")
	}
	if p.sftpClient != nil {
		return p.sftpClient, nil
	}
	sc, err := sftp.NewClient(p.client)
	if err != nil {
		return nil, fmt.Errorf("sshclient: create sftp client: %w", err)
	}
	p.sftpClient = sc
	return sc, nil
}

type connPool struct {
	mu      sync.Mutex
	entries map[string]*pooledConn
	closed  bool

	reaperOnce sync.Once
	stopCh     chan struct{}
}

func newConnPool() *connPool {
	return &connPool{
		entries: make(map[string]*pooledConn),
		stopCh:  make(chan struct{}),
	}
}

func poolKey(req ConnectionRequest) string {
	port := req.Port
	if port <= 0 {
		port = 22
	}
	h := sha256.New()
	write := func(s string) {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	write(strings.ToLower(strings.TrimSpace(req.Host)))
	write(strconv.Itoa(port))
	write(strings.TrimSpace(req.Username))
	write(strings.ToLower(strings.TrimSpace(req.AuthMode)))
	write(req.Password)
	write(req.PrivateKeyPEM)
	write(req.KeyPassphrase)
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Service) ensurePool() *connPool {
	s.poolInit.Do(func() {
		s.pool = newConnPool()
	})
	return s.pool
}

func (p *connPool) startReaper() {
	p.reaperOnce.Do(func() {
		go p.reapLoop()
	})
}

func (p *connPool) reapLoop() {
	ticker := time.NewTicker(poolReapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case now := <-ticker.C:
			p.reapOnce(now)
		}
	}
}

func (p *connPool) reapOnce(now time.Time) {
	cutoff := now.Add(-poolIdleTTL).UnixNano()
	pingCutoff := now.Add(-poolKeepaliveInterval).UnixNano()

	p.mu.Lock()
	var toClose []*pooledConn
	var toPing []*pooledConn
	for k, e := range p.entries {
		if e.inUse.Load() > 0 {
			continue
		}
		if e.closed.Load() || e.lastUsed.Load() < cutoff {
			e.closed.Store(true)
			delete(p.entries, k)
			toClose = append(toClose, e)
			continue
		}
		if e.lastPing.Load() < pingCutoff {
			e.inUse.Add(1)
			toPing = append(toPing, e)
		}
	}
	p.mu.Unlock()

	for _, e := range toClose {
		e.closeNow()
	}

	for _, e := range toPing {
		alive := probeSSHKeepalive(e.client, poolKeepaliveTimeout)
		if !alive {
			p.evict(e)
		} else {
			e.lastPing.Store(time.Now().UnixNano())
		}
		e.release()
	}
}

func probeSSHKeepalive(client *xssh.Client, timeout time.Duration) bool {
	if client == nil {
		return false
	}
	done := make(chan bool, 1)
	go func() {
		_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
		done <- (err == nil)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case ok := <-done:
		return ok
	case <-timer.C:
		return false
	}
}

func (p *connPool) evict(entry *pooledConn) {
	if p == nil || entry == nil {
		return
	}
	p.mu.Lock()
	if cur, ok := p.entries[entry.key]; ok && cur == entry {
		delete(p.entries, entry.key)
	}
	p.mu.Unlock()
	entry.closed.Store(true)
	if entry.inUse.Load() <= 0 {
		entry.closeNow()
	}
}

func (p *connPool) evictAddress(address string) {
	if p == nil || address == "" {
		return
	}
	p.mu.Lock()
	var victims []*pooledConn
	for k, e := range p.entries {
		if strings.EqualFold(e.address, address) {
			e.closed.Store(true)
			delete(p.entries, k)
			victims = append(victims, e)
		}
	}
	p.mu.Unlock()
	for _, e := range victims {
		if e.inUse.Load() <= 0 {
			e.closeNow()
		}
	}
}

func (p *connPool) closeAll() {
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	close(p.stopCh)
	victims := make([]*pooledConn, 0, len(p.entries))
	for k, e := range p.entries {
		e.closed.Store(true)
		delete(p.entries, k)
		victims = append(victims, e)
	}
	p.mu.Unlock()
	for _, e := range victims {
		e.closeNow()
	}
}

// acquirePooled returns an authenticated pooled connection (reused=true when
// borrowed from cache, false when freshly dialed). Callers must invoke
// entry.release() when finished.
func (s *Service) acquirePooled(ctx context.Context, req ConnectionRequest) (entry *pooledConn, reused bool, err error) {
	p := s.ensurePool()
	key := poolKey(req)

	p.mu.Lock()
	if !p.closed {
		if existing, ok := p.entries[key]; ok && !existing.closed.Load() {
			existing.inUse.Add(1)
			p.mu.Unlock()
			return existing, true, nil
		}
	}
	p.mu.Unlock()

	client, address, err := s.connect(ctx, req)
	if err != nil {
		return nil, false, err
	}

	nowNano := time.Now().UnixNano()
	fresh := &pooledConn{
		key:     key,
		address: address,
		client:  client,
	}
	fresh.inUse.Store(1)
	fresh.lastUsed.Store(nowNano)
	fresh.lastPing.Store(nowNano)

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		fresh.closed.Store(true)
		return fresh, false, nil
	}
	if prev, ok := p.entries[key]; ok && prev != fresh {
		prev.closed.Store(true)
		if prev.inUse.Load() <= 0 {
			go prev.closeNow()
		}
	}
	p.entries[key] = fresh
	p.mu.Unlock()
	p.startReaper()

	return fresh, false, nil
}

// EvictHost removes any cached pooled SSH/SFTP connections targeting host:port.
// Call this when server credentials change, a server is deleted, or a host key
// is forgotten.
func (s *Service) EvictHost(host string, port int) {
	if s == nil || s.pool == nil {
		return
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return
	}
	if port <= 0 {
		port = 22
	}
	s.pool.evictAddress(net.JoinHostPort(host, strconv.Itoa(port)))
}

// Close tears down all pooled SSH/SFTP connections and stops the background
// keepalive/idle reaper. Safe to call multiple times.
func (s *Service) Close() error {
	if s == nil || s.pool == nil {
		return nil
	}
	s.pool.closeAll()
	return nil
}
