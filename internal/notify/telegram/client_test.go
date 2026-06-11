package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testToken = "123456:secret-token"

func TestSendSuccess(t *testing.T) {
	var gotPath, gotChatID, gotText string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotPath = r.URL.Path
		gotChatID = r.FormValue("chat_id")
		gotText = r.FormValue("text")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer server.Close()

	client := NewClient(testToken, WithBaseURL(server.URL))
	if err := client.Send(context.Background(), "-100123", "hello"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if gotPath != "/bot"+testToken+"/sendMessage" {
		t.Fatalf("request path = %q", gotPath)
	}
	if gotChatID != "-100123" || gotText != "hello" {
		t.Fatalf("chat_id=%q text=%q", gotChatID, gotText)
	}
}

func TestSendAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
	}))
	defer server.Close()

	client := NewClient(testToken, WithBaseURL(server.URL))
	err := client.Send(context.Background(), "-100123", "hello")
	if err == nil {
		t.Fatal("expected an error for a non-OK API response")
	}
	if !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("error should carry the API description, got %v", err)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("error leaked the bot token: %v", err)
	}
}

func TestSendTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := NewClient(testToken, WithBaseURL(server.URL), WithTimeout(20*time.Millisecond))
	err := client.Send(context.Background(), "-100123", "hello")
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("timeout error leaked the bot token: %v", err)
	}
}

func TestSendRequiresTokenAndChatID(t *testing.T) {
	if err := NewClient("").Send(context.Background(), "-100", "hi"); err == nil {
		t.Fatal("expected error when token is empty")
	}
	if err := NewClient(testToken).Send(context.Background(), "  ", "hi"); err == nil {
		t.Fatal("expected error when chat id is empty")
	}
}
