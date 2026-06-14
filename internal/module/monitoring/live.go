package monitoring

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	cwebsocket "github.com/coder/websocket"

	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/livemetrics"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
)

// Live metrics WebSocket.
//
// The page is already gated by the auth middleware (the upgrade carries the
// session cookie), so authentication is established before this handler runs.
// On top of that we enforce a same-origin check (WS upgrades bypass the CSRF
// middleware) and a per-user concurrent-socket cap for abuse prevention, and we
// refuse servers without stored credentials (live collection resolves SSH
// credentials server-side — it never accepts them from the client).
//
// Server → client frames (JSON):
//
//	{"type":"metrics","data":<Metrics>}
//	{"type":"error","message":"<reason>"}   // transient; client shows "reconnecting"
const (
	// maxLiveSocketsPerUser caps concurrent live-metric WebSockets per account.
	maxLiveSocketsPerUser = 8
	// liveWriteTimeout drops a client that cannot keep up rather than buffering.
	liveWriteTimeout = 5 * time.Second
)

type LiveHandler struct {
	deps       module.Dependencies
	serverRepo servers.Repository
	hub        *livemetrics.Hub
}

func NewLiveHandler(deps module.Dependencies, serverRepo servers.Repository, hub *livemetrics.Hub) LiveHandler {
	return LiveHandler{deps: deps, serverRepo: serverRepo, hub: hub}
}

func (h LiveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// WS upgrades skip the CSRF middleware, so replicate its same-origin guard.
	if err := middleware.ValidateSameOriginRequest(r); err != nil {
		http.Error(w, "monitoring: cross-origin WebSocket rejected", http.StatusForbidden)
		return
	}

	serverID, ok := pathID(r)
	if !ok {
		http.Error(w, "monitoring: server not found", http.StatusNotFound)
		return
	}
	server, err := h.serverRepo.GetByID(r.Context(), serverID)
	if err != nil {
		http.Error(w, "monitoring: server not found", http.StatusNotFound)
		return
	}
	if !servers.HasStoredCredentials(server) {
		http.Error(w, "monitoring: live metrics require stored SSH credentials", http.StatusBadRequest)
		return
	}

	// Per-user concurrency cap (reject before upgrading so the error is plain).
	user := middleware.GetAuthenticatedUser(r.Context())
	if !h.hub.TryAcquire(user, maxLiveSocketsPerUser) {
		http.Error(w, "monitoring: too many live metric sessions open", http.StatusTooManyRequests)
		return
	}
	defer h.hub.Release(user)

	conn, err := cwebsocket.Accept(w, r, &cwebsocket.AcceptOptions{
		InsecureSkipVerify: true, // same-origin already validated above
	})
	if err != nil {
		return
	}
	defer conn.Close(cwebsocket.StatusNormalClosure, "stream ended")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	password, privateKey, keyPassphrase := servers.ResolveCredentials(server)
	connReq := sshclient.ConnectionRequest{
		Host:           server.Host,
		Port:           server.Port,
		Username:       server.Username,
		AuthMode:       server.AuthMode,
		Password:       password,
		PrivateKeyPEM:  privateKey,
		KeyPassphrase:  keyPassphrase,
		ConnectTimeout: h.deps.Config.SSH.ConnectTimeout,
	}

	sub := h.hub.Subscribe(serverID, connReq)
	defer sub.Close()

	// Drain client → server frames; a read error means the client went away.
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case update, ok := <-sub.C:
			if !ok {
				return
			}
			if err := writeLiveUpdate(ctx, conn, update); err != nil {
				return
			}
		}
	}
}

// writeLiveUpdate encodes one update as a JSON frame and writes it with a bound
// deadline so a stalled client tears the session down instead of buffering.
func writeLiveUpdate(ctx context.Context, conn *cwebsocket.Conn, update livemetrics.Update) error {
	var payload []byte
	var err error
	if update.Metrics != nil {
		payload, err = json.Marshal(struct {
			Type string               `json:"type"`
			Data *livemetrics.Metrics `json:"data"`
		}{"metrics", update.Metrics})
	} else {
		payload, err = json.Marshal(struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		}{"error", update.Error})
	}
	if err != nil {
		return err
	}

	writeCtx, cancel := context.WithTimeout(ctx, liveWriteTimeout)
	defer cancel()
	return conn.Write(writeCtx, cwebsocket.MessageText, payload)
}

func liveURL(serverID int64) string {
	return monitoringURL(serverID) + "/live"
}
