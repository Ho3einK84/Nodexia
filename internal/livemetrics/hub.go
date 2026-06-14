package livemetrics

import (
	"context"
	"sync"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/sshclient"
)

const (
	// DefaultInterval is how often the remote loop emits a metrics frame.
	DefaultInterval = 3 * time.Second
	// defaultIdleGrace keeps a broker (and its one SSH connection) warm briefly
	// after the last subscriber leaves, so a page refresh or quick reconnect
	// reuses the connection instead of paying a fresh handshake.
	defaultIdleGrace = 8 * time.Second
	// Reconnect backoff bounds for a dropped collection stream.
	minBackoff = 1 * time.Second
	maxBackoff = 15 * time.Second
	// subscriberBuffer is the per-client channel depth. Latest-wins: a slow
	// client's stale frame is dropped rather than blocking collection.
	subscriberBuffer = 1
)

// StreamFunc runs the remote collection loop, invoking onLine for each stdout
// line until ctx is cancelled or the remote command exits. The default wraps
// sshclient.Service.StreamScan; tests inject a fake. onLine is always called
// from the goroutine that invoked StreamFunc (synchronous), so the broker can
// mutate per-stream state inside it without extra locking.
type StreamFunc func(ctx context.Context, conn sshclient.ConnectionRequest, onLine func(string)) error

// Hub fans out live metrics for many servers. It keeps at most one broker per
// server; every client watching a server shares that broker's single SSH
// connection.
type Hub struct {
	stream    StreamFunc
	interval  time.Duration
	idleGrace time.Duration

	mu      sync.Mutex
	brokers map[int64]*broker

	sessMu   sync.Mutex
	sessions map[string]int
}

// New builds a Hub backed by the SSH service. The collection loop is shared per
// server, so the connection count equals the number of distinctly-watched
// servers — never the number of clients.
func New(ssh *sshclient.Service) *Hub {
	return NewWithStream(func(ctx context.Context, conn sshclient.ConnectionRequest, onLine func(string)) error {
		return ssh.StreamScan(ctx, sshclient.CommandRequest{
			ConnectionRequest: conn,
			Command:           collectCommand(DefaultInterval),
		}, onLine)
	})
}

// NewWithStream builds a Hub with a custom stream function (used by tests).
func NewWithStream(stream StreamFunc) *Hub {
	return &Hub{
		stream:    stream,
		interval:  DefaultInterval,
		idleGrace: defaultIdleGrace,
		brokers:   map[int64]*broker{},
		sessions:  map[string]int{},
	}
}

// Interval reports the live sampling cadence (for display in the UI).
func (h *Hub) Interval() time.Duration { return h.interval }

// Subscribe registers a client for a server's live metrics, starting the
// shared broker if it is the first subscriber. The returned Subscription must
// be closed by the caller.
func (h *Hub) Subscribe(serverID int64, conn sshclient.ConnectionRequest) *Subscription {
	h.mu.Lock()
	b := h.brokers[serverID]
	if b == nil {
		ctx, cancel := context.WithCancel(context.Background())
		b = &broker{
			hub:      h,
			serverID: serverID,
			conn:     conn,
			ctx:      ctx,
			cancel:   cancel,
			subs:     map[*Subscription]struct{}{},
		}
		h.brokers[serverID] = b
		go b.run()
	}
	sub := b.addLocked()
	h.mu.Unlock()

	b.pushLatest(sub)
	return sub
}

// stopBroker cancels and unregisters a broker if it is still idle. Called from
// the idle timer scheduled when the last subscriber left.
func (h *Hub) stopBroker(b *broker) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.brokers[b.serverID] != b {
		return
	}
	b.mu.Lock()
	idle := len(b.subs) == 0
	b.mu.Unlock()
	if !idle {
		return
	}
	delete(h.brokers, b.serverID)
	b.cancel()
}

// activeBrokers reports how many servers currently have a live collection
// running (used by tests).
func (h *Hub) activeBrokers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.brokers)
}

// TryAcquire reserves one live-socket slot for user, returning false (without
// reserving) when the per-user limit is already reached. This caps how many
// concurrent live streams a single account can open (abuse prevention).
func (h *Hub) TryAcquire(user string, max int) bool {
	h.sessMu.Lock()
	defer h.sessMu.Unlock()
	if h.sessions[user] >= max {
		return false
	}
	h.sessions[user]++
	return true
}

// Release returns one live-socket slot for user.
func (h *Hub) Release(user string) {
	h.sessMu.Lock()
	defer h.sessMu.Unlock()
	if h.sessions[user] > 0 {
		h.sessions[user]--
	}
}

// broker owns one server's shared collection loop and its subscribers.
type broker struct {
	hub      *Hub
	serverID int64
	conn     sshclient.ConnectionRequest

	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex
	subs      map[*Subscription]struct{}
	latest    *Metrics
	stopTimer *time.Timer
}

// run drives the collection loop, reconnecting with capped backoff while the
// broker has not been cancelled. The parser is reset on every (re)connect so a
// half-read frame from a dropped connection never bleeds into the next one.
func (b *broker) run() {
	backoff := minBackoff
	parser := &frameParser{}
	for b.ctx.Err() == nil {
		err := b.hub.stream(b.ctx, b.conn, func(line string) {
			if m, ok := parser.line(line); ok {
				backoff = minBackoff
				b.publish(Update{Metrics: m})
			}
		})
		if b.ctx.Err() != nil {
			return
		}

		msg := "live metrics stream ended; reconnecting"
		if err != nil {
			msg = err.Error()
		}
		b.publish(Update{Error: msg})

		select {
		case <-b.ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
		parser = &frameParser{}
	}
}

// publish fans an update out to every subscriber and caches the latest metrics.
// Delivery is latest-wins and never blocks: if a subscriber's buffer is full,
// the stale frame is discarded and the new one substituted.
func (b *broker) publish(u Update) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if u.Metrics != nil {
		b.latest = u.Metrics
	}
	for sub := range b.subs {
		select {
		case sub.C <- u:
		default:
			select {
			case <-sub.C:
			default:
			}
			select {
			case sub.C <- u:
			default:
			}
		}
	}
}

// addLocked registers a subscriber. The hub mutex is held by the caller, which
// (together with stopBroker taking the same mutex) guarantees a broker about to
// be stopped cannot gain a subscriber that never receives frames.
func (b *broker) addLocked() *Subscription {
	sub := &Subscription{C: make(chan Update, subscriberBuffer), broker: b}
	b.mu.Lock()
	if b.stopTimer != nil {
		b.stopTimer.Stop()
		b.stopTimer = nil
	}
	b.subs[sub] = struct{}{}
	b.mu.Unlock()
	return sub
}

// pushLatest delivers the cached most-recent frame to a freshly added
// subscriber so the client renders immediately instead of waiting an interval.
func (b *broker) pushLatest(sub *Subscription) {
	b.mu.Lock()
	latest := b.latest
	b.mu.Unlock()
	if latest != nil {
		select {
		case sub.C <- Update{Metrics: latest}:
		default:
		}
	}
}

// remove drops a subscriber and schedules an idle stop when it was the last.
func (b *broker) remove(sub *Subscription) {
	b.mu.Lock()
	if _, ok := b.subs[sub]; !ok {
		b.mu.Unlock()
		return
	}
	delete(b.subs, sub)
	close(sub.C)
	if len(b.subs) == 0 && b.stopTimer == nil {
		b.stopTimer = time.AfterFunc(b.hub.idleGrace, func() { b.hub.stopBroker(b) })
	}
	b.mu.Unlock()
}

// Subscription is one client's view of a server's live metrics. Read from C;
// it is closed when the subscription ends. Call Close exactly once when done.
type Subscription struct {
	C      chan Update
	broker *broker
	once   sync.Once
}

// Close detaches the subscription from its broker. Safe to call more than once.
func (s *Subscription) Close() {
	s.once.Do(func() { s.broker.remove(s) })
}
