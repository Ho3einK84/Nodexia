package files_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/files"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
	"github.com/Ho3einK84/Nodexia/internal/view"
	"github.com/pkg/sftp"
	xssh "golang.org/x/crypto/ssh"
)

func startMockSFTPSSHServer(t *testing.T, workDir, password string) (string, int) {
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
				go xssh.DiscardRequests(reqs)
				for newChan := range chans {
					if newChan.ChannelType() != "session" {
						_ = newChan.Reject(xssh.UnknownChannelType, "unknown channel type")
						continue
					}
					ch, requests, err := newChan.Accept()
					if err != nil {
						return
					}
					go func(in <-chan *xssh.Request, channel xssh.Channel) {
						defer func() { _ = channel.Close() }()
						for req := range in {
							if req.Type == "subsystem" && len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp" {
								_ = req.Reply(true, nil)
								srv, serr := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(workDir))
								if serr == nil {
									_ = srv.Serve()
									_ = srv.Close()
								}
								return
							}
							_ = req.Reply(false, nil)
						}
					}(requests, ch)
				}
			}(rawConn)
		}
	}()

	tcpAddr := ln.Addr().(*net.TCPAddr)
	return tcpAddr.IP.String(), tcpAddr.Port
}

func newFilesTestHarness(t *testing.T, host string, port int, password string) (*http.ServeMux, int64) {
	t.Helper()
	runtime := testutil.OpenTestDB(t)
	cfg := testutil.TestConfig(t)
	cfg.Security.SSHHostKeyPolicy = "insecure"
	renderer, err := view.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	deps := module.Dependencies{
		Config:   cfg,
		Database: runtime,
		SSH:      sshclient.New(cfg.SSH, cfg.Security),
		Renderer: renderer,
	}

	repo := servers.NewSQLRepository(runtime.SQL)
	srv, err := repo.Create(context.Background(), servers.Server{
		Name:               "mock-sftp",
		Host:               host,
		Port:               port,
		AuthMode:           servers.AuthModePassword,
		Username:           "root",
		CredentialStrategy: servers.CredentialStrategyStored,
		CredentialRef:      password,
	})
	if err != nil {
		t.Fatalf("Create server: %v", err)
	}

	mux := http.NewServeMux()
	files.New().RegisterRoutes(mux, deps)
	return mux, srv.ID
}

func postJSONOp(t *testing.T, mux *http.ServeMux, serverID int64, intent string, payload any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/servers/"+strconv.FormatInt(serverID, 10)+"/files/ops?intent="+intent, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestFilesModuleEndToEndSFTP(t *testing.T) {
	workDir := t.TempDir()
	const pass = "sftp-test-pass"
	host, port := startMockSFTPSSHServer(t, workDir, pass)
	mux, serverID := newFilesTestHarness(t, host, port, pass)
	baseURL := "/servers/" + strconv.FormatInt(serverID, 10) + "/files"

	// Seed a dotfile and a 1x1 PNG file
	if err := os.WriteFile(filepath.Join(workDir, ".env"), []byte("APP=nodexia\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	png1x1 := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
	}
	if err := os.WriteFile(filepath.Join(workDir, "logo.png"), png1x1, 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. GET page renders SSR listing
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, baseURL+"?path="+url.QueryEscape(workDir), nil))
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET page status = %d: %s", getRec.Code, getRec.Body.String())
	}
	if !strings.Contains(getRec.Body.String(), "logo.png") || !strings.Contains(getRec.Body.String(), ".env") {
		t.Fatal("GET page missing seeded directory entries")
	}

	// 2. JSON list
	code, listRes := postJSONOp(t, mux, serverID, "list", map[string]any{"path": workDir})
	if code != http.StatusOK || listRes["ok"] != true {
		t.Fatalf("list failed: code=%d res=%v", code, listRes)
	}

	// 3. Nested mkdir
	code, mkRes := postJSONOp(t, mux, serverID, "mkdir", map[string]any{
		"path":      workDir,
		"name":      "configs/nginx",
		"recursive": true,
	})
	if code != http.StatusOK || mkRes["ok"] != true {
		t.Fatalf("mkdir nested failed: code=%d res=%v", code, mkRes)
	}

	// 4. Write new file + Read back + Chmod
	confPath := filepath.Join(workDir, "configs", "nginx", "site.conf")
	code, wrRes := postJSONOp(t, mux, serverID, "write", map[string]any{
		"path":        confPath,
		"content":     "listen 443;\n",
		"create_only": true,
	})
	if code != http.StatusOK || wrRes["ok"] != true {
		t.Fatalf("write create_only failed: code=%d res=%v", code, wrRes)
	}

	code, rdRes := postJSONOp(t, mux, serverID, "read", map[string]any{"path": confPath})
	if code != http.StatusOK || rdRes["ok"] != true {
		t.Fatalf("read text failed: code=%d res=%v", code, rdRes)
	}
	fileObj := rdRes["file"].(map[string]any)
	if fileObj["content"] != "listen 443;\n" || fileObj["editable"] != true {
		t.Fatalf("unexpected read text file: %v", fileObj)
	}

	// Read image returns data_url
	code, imgRes := postJSONOp(t, mux, serverID, "read", map[string]any{"path": filepath.Join(workDir, "logo.png")})
	if code != http.StatusOK || imgRes["ok"] != true {
		t.Fatalf("read image failed: code=%d res=%v", code, imgRes)
	}
	imgObj := imgRes["file"].(map[string]any)
	if imgObj["is_image"] != true || !strings.HasPrefix(imgObj["data_url"].(string), "data:image/png;base64,") {
		t.Fatalf("unexpected image preview payload: %v", imgObj)
	}

	// Chmod
	code, cmRes := postJSONOp(t, mux, serverID, "chmod", map[string]any{"path": confPath, "mode": "0750"})
	if code != http.StatusOK || cmRes["ok"] != true {
		t.Fatalf("chmod failed: code=%d res=%v", code, cmRes)
	}

	// 5. Folder upload with relative_path
	var upBuf bytes.Buffer
	mw := multipart.NewWriter(&upBuf)
	_ = mw.WriteField("path", workDir)
	_ = mw.WriteField("relative_path", "bundle/sub/hello.txt")
	fw, _ := mw.CreateFormFile("file", "hello.txt")
	_, _ = fw.Write([]byte("uploaded via folder"))
	_ = mw.Close()
	upReq := httptest.NewRequest(http.MethodPost, baseURL+"/ops?intent=upload", &upBuf)
	upReq.Header.Set("Content-Type", mw.FormDataContentType())
	upRec := httptest.NewRecorder()
	mux.ServeHTTP(upRec, upReq)
	if upRec.Code != http.StatusOK {
		t.Fatalf("folder upload failed: %d %s", upRec.Code, upRec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(workDir, "bundle", "sub", "hello.txt")); err != nil {
		t.Fatalf("uploaded nested file missing: %v", err)
	}

	// Reject relative_path traversal
	var badUp bytes.Buffer
	bmw := multipart.NewWriter(&badUp)
	_ = bmw.WriteField("path", workDir)
	_ = bmw.WriteField("relative_path", "../escape.txt")
	bfw, _ := bmw.CreateFormFile("file", "escape.txt")
	_, _ = bfw.Write([]byte("bad"))
	_ = bmw.Close()
	badReq := httptest.NewRequest(http.MethodPost, baseURL+"/ops?intent=upload", &badUp)
	badReq.Header.Set("Content-Type", bmw.FormDataContentType())
	badRec := httptest.NewRecorder()
	mux.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on relative_path traversal, got %d", badRec.Code)
	}

	// 6. Symlink + Stat + Search + Copy + Move + Compress + Extract + Batch Delete
	code, symRes := postJSONOp(t, mux, serverID, "symlink", map[string]any{
		"path":   workDir,
		"name":   "site.link",
		"target": confPath,
	})
	if code != http.StatusOK || symRes["ok"] != true {
		t.Fatalf("symlink failed: code=%d res=%v", code, symRes)
	}

	code, statRes := postJSONOp(t, mux, serverID, "stat", map[string]any{
		"path":      filepath.Join(workDir, "configs"),
		"calc_size": true,
	})
	if code != http.StatusOK || statRes["ok"] != true {
		t.Fatalf("stat failed: code=%d res=%v", code, statRes)
	}
	statObj := statRes["stat"].(map[string]any)
	if int(statObj["dir_files"].(float64)) != 1 {
		t.Fatalf("expected dir_files=1, got %v", statObj["dir_files"])
	}

	code, searchRes := postJSONOp(t, mux, serverID, "search", map[string]any{
		"path":           workDir,
		"query":          "listen 443",
		"search_content": true,
		"include_hidden": false,
	})
	if code != http.StatusOK || searchRes["ok"] != true {
		t.Fatalf("search failed: code=%d res=%v", code, searchRes)
	}
	if len(searchRes["matches"].([]any)) != 1 {
		t.Fatalf("expected 1 search match, got %v", searchRes["matches"])
	}

	// Compress + Extract
	code, compRes := postJSONOp(t, mux, serverID, "compress", map[string]any{
		"path":   workDir,
		"names":  []string{"configs"},
		"name":   "cfg",
		"format": "zip",
	})
	if code != http.StatusOK || compRes["ok"] != true {
		t.Fatalf("compress failed: code=%d res=%v", code, compRes)
	}

	extractDest := filepath.Join(workDir, "extracted")
	code, extRes := postJSONOp(t, mux, serverID, "extract", map[string]any{
		"path": filepath.Join(workDir, "cfg.zip"),
		"dest": extractDest,
	})
	if code != http.StatusOK || extRes["ok"] != true {
		t.Fatalf("extract failed: code=%d res=%v", code, extRes)
	}

	// 7. Archive download via ActionHandler
	form := url.Values{}
	form.Set("intent", "archive_download")
	form.Set("format", "zip")
	form.Set("name", "bundle")
	form.Set("paths", `["`+filepath.Join(workDir, "configs")+`","`+filepath.Join(workDir, ".env")+`"]`)
	dlReq := httptest.NewRequest(http.MethodPost, baseURL, strings.NewReader(form.Encode()))
	dlReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	dlRec := httptest.NewRecorder()
	mux.ServeHTTP(dlRec, dlReq)
	if dlRec.Code != http.StatusOK {
		t.Fatalf("archive_download status = %d", dlRec.Code)
	}
	zr, err := zip.NewReader(bytes.NewReader(dlRec.Body.Bytes()), int64(dlRec.Body.Len()))
	if err != nil {
		t.Fatalf("invalid zip from archive_download: %v", err)
	}
	if len(zr.File) < 2 {
		t.Fatalf("expected >= 2 zip entries, got %d", len(zr.File))
	}

	// 8. Batch delete refuses root /
	code, rootDel := postJSONOp(t, mux, serverID, "delete", map[string]any{
		"paths":     []string{"/"},
		"recursive": true,
	})
	if code != http.StatusUnprocessableEntity || rootDel["ok"] != false {
		t.Fatalf("expected 422 on root delete, got code=%d res=%v", code, rootDel)
	}

	// Copy + Move + Batch delete
	code, cpRes := postJSONOp(t, mux, serverID, "copy", map[string]any{
		"paths":     []string{filepath.Join(workDir, ".env")},
		"dest":      workDir,
		"duplicate": true,
	})
	if code != http.StatusOK || cpRes["ok"] != true {
		t.Fatalf("duplicate copy failed: code=%d res=%v", code, cpRes)
	}
	code, mvRes := postJSONOp(t, mux, serverID, "move", map[string]any{
		"paths": []string{filepath.Join(workDir, ".env_copy")},
		"dest":  extractDest,
	})
	if code != http.StatusOK || mvRes["ok"] != true {
		t.Fatalf("move failed: code=%d res=%v", code, mvRes)
	}
	code, delRes := postJSONOp(t, mux, serverID, "delete", map[string]any{
		"paths":     []string{filepath.Join(workDir, "site.link"), extractDest},
		"recursive": true,
	})
	if code != http.StatusOK || delRes["ok"] != true {
		t.Fatalf("batch delete failed: code=%d res=%v", code, delRes)
	}
	// Target of unlinked symlink MUST still exist!
	if _, err := os.Stat(confPath); err != nil {
		t.Fatalf("unlinking symlink deleted target file: %v", err)
	}
	_ = io.Discard
}
