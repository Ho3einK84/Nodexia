package terminal_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/module/terminal"
)

func TestTerminalUpload_UnauthorizedWithoutSessionOrCreds(t *testing.T) {
	deps := newTestDeps(t)
	repo := servers.NewSQLRepository(deps.Database.SQL)
	server := seedServer(t, repo, false) // no stored credentials

	mux := http.NewServeMux()
	terminal.New().RegisterRoutes(mux, deps)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "test.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte("hello world")); err != nil {
		t.Fatalf("write to part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/servers/"+strconv.FormatInt(server.ID, 10)+"/terminal/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d: %s", rec.Code, rec.Body.String())
	}
}
