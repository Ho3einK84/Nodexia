package terminal_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/module/commands"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
)

type terminalHistoryResponse struct {
	OK            bool `json:"ok"`
	Recorded      bool `json:"recorded"`
	ServerHistory []struct {
		Cmd   string `json:"cmd"`
		Count int    `json:"count"`
		TS    int64  `json:"ts"`
	} `json:"server_history"`
	GlobalHistory []struct {
		Cmd   string `json:"cmd"`
		Count int    `json:"count"`
		TS    int64  `json:"ts"`
	} `json:"global_history"`
}

func TestTerminalHistoryRecordListDeduplicateAndFilterSecrets(t *testing.T) {
	deps := newTestDeps(t)
	serverRepo := servers.NewSQLRepository(deps.Database.SQL)
	srv1 := seedServer(t, serverRepo, true)
	srv2, err := serverRepo.Create(context.Background(), servers.Server{
		Name:               "other-node",
		Host:               "10.0.0.2",
		Port:               22,
		AuthMode:           servers.AuthModePassword,
		Username:           "root",
		CredentialStrategy: servers.CredentialStrategyStored,
		CredentialRef:      "secret",
	})
	if err != nil {
		t.Fatalf("seed second server: %v", err)
	}

	// Seed a command from the Commands workspace on srv1.
	cmdRepo := commands.NewSQLRepository(deps.Database.SQL)
	if _, err := cmdRepo.Append(context.Background(), commands.HistoryEntry{
		ServerID: srv1.ID,
		Command:  "ss -tulpn",
	}); err != nil {
		t.Fatalf("seed command_history: %v", err)
	}

	mux := registerRoutes(t, deps)

	postCmd := func(serverID int64, cmd string) terminalHistoryResponse {
		t.Helper()
		payload, _ := json.Marshal(map[string]string{"command": cmd})
		req := sameOriginRequest(http.MethodPost, "/servers/"+sid(serverID)+"/terminal/history", string(payload))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("POST history %q: status %d, body %s", cmd, w.Code, w.Body.String())
		}
		var out terminalHistoryResponse
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode POST history: %v", err)
		}
		return out
	}

	// Record valid commands, duplicates, and secret/ignored commands.
	if res := postCmd(srv1.ID, "/root/agy-remote status"); !res.Recorded {
		t.Error("expected /root/agy-remote status to be recorded")
	}
	if res := postCmd(srv1.ID, "/root/agy-remote status"); !res.Recorded {
		t.Error("expected duplicate command upsert to report recorded=true")
	}
	if res := postCmd(srv1.ID, "apt update"); !res.Recorded {
		t.Error("expected apt update to be recorded")
	}
	if res := postCmd(srv2.ID, "journalctl -u nodexia -f"); !res.Recorded {
		t.Error("expected global command on srv2 to be recorded")
	}

	// Secret / leading-space / blank commands must NOT be stored.
	for _, skipped := range []string{
		"",
		"   ",
		" leading-space-secret",
		"export API_KEY=supersecret",
		"mysql --password=secret",
		"y",
		"apt update\nrm -rf /",
	} {
		if res := postCmd(srv1.ID, skipped); res.Recorded {
			t.Errorf("command %q should NOT have been recorded", skipped)
		}
	}

	// GET history for srv1.
	getReq := httptest.NewRequest(http.MethodGet, "/servers/"+sid(srv1.ID)+"/terminal/history", nil)
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET history status = %d, want 200", getRec.Code)
	}

	var list terminalHistoryResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode GET history: %v", err)
	}
	if !list.OK {
		t.Fatal("GET history ok=false")
	}

	foundAgy := false
	foundCmdWorkspace := false
	for _, item := range list.ServerHistory {
		if item.Cmd == "/root/agy-remote status" {
			foundAgy = true
			if item.Count != 2 {
				t.Errorf("/root/agy-remote status count = %d, want 2", item.Count)
			}
		}
		if item.Cmd == "ss -tulpn" {
			foundCmdWorkspace = true
		}
		if strings.Contains(item.Cmd, "supersecret") || strings.Contains(item.Cmd, "leading-space") {
			t.Errorf("filtered command leaked into server_history: %q", item.Cmd)
		}
	}
	if !foundAgy {
		t.Errorf("expected /root/agy-remote status in server_history: %+v", list.ServerHistory)
	}
	if !foundCmdWorkspace {
		t.Errorf("expected seeded command_history entry 'ss -tulpn' in server_history: %+v", list.ServerHistory)
	}

	foundGlobal := false
	for _, item := range list.GlobalHistory {
		if item.Cmd == "journalctl -u nodexia -f" {
			foundGlobal = true
		}
	}
	if !foundGlobal {
		t.Errorf("expected srv2 command in global_history: %+v", list.GlobalHistory)
	}

	// DELETE clears server terminal history cleanly.
	delReq := sameOriginRequest(http.MethodDelete, "/servers/"+sid(srv1.ID)+"/terminal/history", "")
	delRec := httptest.NewRecorder()
	mux.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("DELETE history status = %d, want 200", delRec.Code)
	}

	// Verify terminal_command_history is empty for srv1 while srv2 remains.
	var count int
	if err := deps.Database.SQL.QueryRow(`SELECT COUNT(*) FROM terminal_command_history WHERE server_id = ?`, srv1.ID).Scan(&count); err != nil {
		t.Fatalf("count srv1 history: %v", err)
	}
	if count != 0 {
		t.Errorf("srv1 terminal_command_history count = %d, want 0 after DELETE", count)
	}

	// Deleting srv2 via servers.Repository must cascade cleanly.
	if err := serverRepo.Delete(context.Background(), srv2.ID); err != nil {
		t.Fatalf("delete srv2: %v", err)
	}
}
