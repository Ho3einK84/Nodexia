package nodes

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/commandstream"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
	"github.com/Ho3einK84/Nodexia/internal/view"
	xssh "golang.org/x/crypto/ssh"
)

type stubServerRepo struct {
	servers.Repository
	getFn func(context.Context, int64) (servers.Server, error)
}

func (s stubServerRepo) GetByID(ctx context.Context, id int64) (servers.Server, error) {
	return s.getFn(ctx, id)
}

func newNodeTestEnv(t *testing.T, serverRepo servers.Repository, repo Repository, sshSvc *sshclient.Service) (*Handlers, *http.ServeMux, module.Dependencies) {
	t.Helper()
	runtime := testutil.OpenTestDB(t)
	cfg := testutil.TestConfig(t)
	cfg.Security.SSHHostKeyPolicy = "insecure"
	renderer, err := view.NewRenderer()
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}
	if sshSvc == nil {
		sshSvc = sshclient.New(cfg.SSH, cfg.Security)
	}
	if serverRepo == nil {
		serverRepo = servers.NewSQLRepository(runtime.SQL)
	}
	if repo == nil {
		repo = NewSQLRepository(runtime.SQL)
	}
	deps := module.Dependencies{
		Config:         cfg,
		Database:       runtime,
		SSH:            sshSvc,
		CommandStreams: commandstream.New(0),
		Renderer:       renderer,
	}
	h := NewHandlers(deps, serverRepo, repo, DefaultProviders())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /servers/{id}/nodes", h.Page)
	mux.HandleFunc("POST /servers/{id}/nodes", h.Refresh)
	mux.HandleFunc("POST /servers/{id}/nodes/actions", h.Action)
	mux.HandleFunc("GET /servers/{id}/nodes/stream/{stream}/events", h.NodeStreamEvents)
	mux.HandleFunc("GET /servers/{id}/nodes/credentials", h.Credentials)
	mux.HandleFunc("GET /servers/{id}/nodes/config", h.ViewConfig)
	mux.HandleFunc("POST /servers/{id}/nodes/install", h.DeployStart)
	mux.HandleFunc("GET /servers/{id}/nodes/install/{job}", h.InstallJob)
	mux.HandleFunc("GET /servers/{id}/nodes/install/{job}/events", h.InstallEvents)
	mux.HandleFunc("POST /servers/{id}/nodes/install/{job}/cancel", h.InstallCancel)
	return h, mux, deps
}

func startFakeNodesSSHServer(t *testing.T, responder func(command string) (stdout string, exit int)) (*net.TCPAddr, string, *atomic.Int32) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519 key: %v", err)
	}
	signer, err := xssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	const pass = "test-ssh-secret"
	cfg := &xssh.ServerConfig{
		PasswordCallback: func(_ xssh.ConnMetadata, p []byte) (*xssh.Permissions, error) {
			if string(p) != pass {
				return nil, errors.New("bad auth")
			}
			return &xssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	var accepts atomic.Int32
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				sconn, chans, reqs, err := xssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				defer func() { _ = sconn.Close() }()
				go xssh.DiscardRequests(reqs)
				for nc := range chans {
					if nc.ChannelType() != "session" {
						_ = nc.Reject(xssh.UnknownChannelType, "session only")
						continue
					}
					ch, sreqs, err := nc.Accept()
					if err != nil {
						continue
					}
					go func(ch xssh.Channel, sreqs <-chan *xssh.Request) {
						defer func() { _ = ch.Close() }()
						for req := range sreqs {
							switch req.Type {
							case "pty-req":
								_ = req.Reply(true, nil)
							case "exec":
								var payload struct{ Command string }
								_ = xssh.Unmarshal(req.Payload, &payload)
								_ = req.Reply(true, nil)
								stdout, code := responder(payload.Command)
								if stdout != "" {
									_, _ = ch.Write([]byte(stdout))
								}
								_, _ = ch.SendRequest("exit-status", false, xssh.Marshal(struct{ Status uint32 }{Status: uint32(code)}))
								return
							default:
								_ = req.Reply(false, nil)
							}
						}
					}(ch, sreqs)
				}
			}(raw)
		}
	}()

	return ln.Addr().(*net.TCPAddr), pass, &accepts
}

func TestPageIsStrictlyReadOnlyNeverDialsSSH(t *testing.T) {
	_, mux, deps := newNodeTestEnv(t, nil, nil, nil)
	serverRepo := servers.NewSQLRepository(deps.Database.SQL)

	addr, pass, dials := startFakeNodesSSHServer(t, func(cmd string) (string, int) {
		return "", 0
	})
	srv, err := serverRepo.Create(context.Background(), servers.Server{
		Name: "readonly-node", Host: addr.IP.String(), Port: addr.Port,
		AuthMode: servers.AuthModePassword, Username: "root",
		CredentialStrategy: servers.CredentialStrategyStored, CredentialRef: pass,
	})
	if err != nil {
		t.Fatalf("Create server: %v", err)
	}

	for _, target := range []string{"/servers/1/nodes", "/servers/1/nodes?refresh=1"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200 (server id %d)", target, w.Code, srv.ID)
		}
	}
	if got := dials.Load(); got != 0 {
		t.Fatalf("GET /nodes triggered %d SSH dials, want 0 (GET must stay read-only)", got)
	}
}

func TestRefreshPOSTPreservesCacheOnTotalFailureAndInspectsConfig(t *testing.T) {
	_, mux, deps := newNodeTestEnv(t, nil, nil, nil)
	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	repo := NewSQLRepository(deps.Database.SQL)

	addr, pass, _ := startFakeNodesSSHServer(t, func(cmd string) (string, int) {
		switch {
		case cmd == (PasarGuardProvider{}).DiscoveryCommand():
			return pgDiscoveryFixture, 0
		case strings.Contains(cmd, "docker logs --tail 250"):
			return "=PGVERSIONS=\npg-node\tStarting Node: v0.5.3\n=PGVERSIONSEND=\n", 0
		case cmd == (RebeccaProvider{}).DiscoveryCommand():
			return rebeccaDiscoveryFixture, 0
		case strings.Contains(cmd, "/opt/pg-node/.env"):
			return "SERVICE_PORT=62050\nAPI_KEY=11111111-2222-3333-4444-555555555555\nSERVICE_PROTOCOL=grpc\n", 0
		default:
			return "", 0
		}
	})

	srv, err := serverRepo.Create(context.Background(), servers.Server{
		Name: "edge-ssh", Host: addr.IP.String(), Port: addr.Port,
		AuthMode: servers.AuthModePassword, Username: "root",
		CredentialStrategy: servers.CredentialStrategyStored, CredentialRef: pass,
	})
	if err != nil {
		t.Fatalf("Create server: %v", err)
	}

	// 1. Clean POST refresh populates snapshots over single pooled SSH session.
	req := httptest.NewRequest(http.MethodPost, "/servers/1/nodes", nil)
	req.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /nodes status = %d: %s", w.Code, w.Body.String())
	}
	var payload struct {
		OK        bool                    `json:"ok"`
		Snapshots []view.NodeSnapshotView `json:"snapshots"`
		Fleet     view.NodeFleetView      `json:"fleet"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode refresh json: %v", err)
	}
	if !payload.OK || payload.Fleet.Total != 4 {
		t.Fatalf("unexpected refresh payload: %+v", payload)
	}

	// 2. ViewConfig redacts API_KEY cleanly while returning non-secret ports.
	cfgReq := httptest.NewRequest(http.MethodGet, "/servers/1/nodes/config?type=pasarguard-node&name=pg-node", nil)
	cfgW := httptest.NewRecorder()
	mux.ServeHTTP(cfgW, cfgReq)
	if cfgW.Code != http.StatusOK {
		t.Fatalf("GET /config status = %d: %s", cfgW.Code, cfgW.Body.String())
	}
	if strings.Contains(cfgW.Body.String(), "11111111-2222-3333-4444-555555555555") {
		t.Fatalf("ViewConfig leaked unredacted API_KEY secret:\n%s", cfgW.Body.String())
	}
	if !strings.Contains(cfgW.Body.String(), `"redacted":true`) || !strings.Contains(cfgW.Body.String(), `"62050"`) {
		t.Fatalf("ViewConfig unexpected body: %s", cfgW.Body.String())
	}

	// 3. Re-point server to unreachable port and run POST refresh -> must NOT wipe stored snapshots!
	srv.Port = 1
	if _, err := serverRepo.Update(context.Background(), srv); err != nil {
		t.Fatalf("Update server: %v", err)
	}
	failReq := httptest.NewRequest(http.MethodPost, "/servers/1/nodes", nil)
	failReq.Header.Set("Accept", "application/json")
	failW := httptest.NewRecorder()
	mux.ServeHTTP(failW, failReq)
	if failW.Code != http.StatusBadGateway {
		t.Fatalf("failed refresh status = %d, want 502", failW.Code)
	}

	stored, err := repo.GetLatestByServer(context.Background(), srv.ID)
	if err != nil || len(stored) != 4 {
		t.Fatalf("total SSH failure wiped/altered persisted snapshots: len=%d err=%v", len(stored), err)
	}
}

func TestCredentialsErrorMappingDistinguishesNotFoundVsDBError(t *testing.T) {
	t.Run("not found returns 404", func(t *testing.T) {
		_, mux, _ := newNodeTestEnv(t, stubServerRepo{
			getFn: func(context.Context, int64) (servers.Server, error) {
				return servers.Server{}, servers.ErrNotFound
			},
		}, nil, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/servers/99/nodes/credentials", nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
	})

	t.Run("db error returns 500", func(t *testing.T) {
		_, mux, _ := newNodeTestEnv(t, stubServerRepo{
			getFn: func(context.Context, int64) (servers.Server, error) {
				return servers.Server{}, errors.New("connection reset by db")
			},
		}, nil, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/servers/99/nodes/credentials", nil))
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", w.Code)
		}
	})
}

func TestDeployStartValidationOverrideAndCancel(t *testing.T) {
	_, mux, deps := newNodeTestEnv(t, nil, nil, nil)
	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	repo := NewSQLRepository(deps.Database.SQL)

	addr, pass, _ := startFakeNodesSSHServer(t, func(cmd string) (string, int) {
		time.Sleep(150 * time.Millisecond)
		return "", 0
	})
	srv, err := serverRepo.Create(context.Background(), servers.Server{
		Name: "deploy-srv", Host: addr.IP.String(), Port: addr.Port,
		AuthMode: servers.AuthModePassword, Username: "root",
		CredentialStrategy: servers.CredentialStrategyStored, CredentialRef: pass,
	})
	if err != nil {
		t.Fatalf("Create server: %v", err)
	}
	if err := repo.ReplaceLatest(context.Background(), srv.ID, []Snapshot{
		{NodeType: pasarguardType, ServiceName: "pg-node", HealthStatus: "running"},
	}, time.Now().UTC()); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	// Unknown engine returns localized error (not raw i18n key).
	badForm := url.Values{"node_type": {"bogus-engine"}}
	badReq := httptest.NewRequest(http.MethodPost, "/servers/1/nodes/install", strings.NewReader(badForm.Encode()))
	badReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badReq.Header.Set("Accept", "application/json")
	badW := httptest.NewRecorder()
	mux.ServeHTTP(badW, badReq)
	if badW.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad engine status = %d, want 422", badW.Code)
	}
	if strings.Contains(badW.Body.String(), `"nodes.err_engine"`) {
		t.Fatalf("raw untranslated i18n key returned: %s", badW.Body.String())
	}

	// Colliding node_name without confirm_override is rejected with 422.
	dupForm := url.Values{"node_type": {pasarguardType}, "node_name": {"pg-node"}, "service_port": {"62050"}, "protocol": {"grpc"}, "channel": {"stable"}}
	dupReq := httptest.NewRequest(http.MethodPost, "/servers/1/nodes/install", strings.NewReader(dupForm.Encode()))
	dupReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	dupReq.Header.Set("Accept", "application/json")
	dupW := httptest.NewRecorder()
	mux.ServeHTTP(dupW, dupReq)
	if dupW.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate name without override status = %d, want 422", dupW.Code)
	}

	// Confirming override starts install job and supports POST cancel.
	okForm := url.Values{"node_type": {pasarguardType}, "node_name": {"pg-node"}, "service_port": {"62050"}, "protocol": {"grpc"}, "channel": {"stable"}, "confirm_override": {"1"}}
	okReq := httptest.NewRequest(http.MethodPost, "/servers/1/nodes/install", strings.NewReader(okForm.Encode()))
	okReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	okReq.Header.Set("Accept", "application/json")
	okW := httptest.NewRecorder()
	mux.ServeHTTP(okW, okReq)
	if okW.Code != http.StatusAccepted {
		t.Fatalf("valid deploy status = %d: %s", okW.Code, okW.Body.String())
	}
	var startResp struct {
		OK        bool   `json:"ok"`
		JobID     string `json:"job_id"`
		CancelURL string `json:"cancel_url"`
	}
	if err := json.Unmarshal(okW.Body.Bytes(), &startResp); err != nil || !startResp.OK || startResp.CancelURL == "" {
		t.Fatalf("unexpected start response: %s (%v)", okW.Body.String(), err)
	}

	cancelW := httptest.NewRecorder()
	mux.ServeHTTP(cancelW, httptest.NewRequest(http.MethodPost, startResp.CancelURL, nil))
	if cancelW.Code != http.StatusOK {
		t.Fatalf("cancel status = %d: %s", cancelW.Code, cancelW.Body.String())
	}
}
