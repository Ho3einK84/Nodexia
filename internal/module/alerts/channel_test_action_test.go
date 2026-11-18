package alerts_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/alerts"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/notify"
	"github.com/Ho3einK84/Nodexia/internal/testutil"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

// fakeNotifier records Send calls and returns a preset error so the channel test
// action can be exercised without contacting Telegram.
type fakeNotifier struct {
	err        error
	calls      int
	lastChatID string
	lastText   string
}

func (f *fakeNotifier) Send(_ context.Context, chatID, text string) error {
	f.calls++
	f.lastChatID = chatID
	f.lastText = text
	return f.err
}

// newChannelTestMux wires a single channel-test route around a real DB/renderer
// with an injectable notifier, and seeds one channel.
func newChannelTestMux(t *testing.T, notifier notify.Notifier) (*http.ServeMux, int64) {
	t.Helper()

	runtime := testutil.OpenTestDB(t)
	cfg := testutil.TestConfig(t)
	renderer, err := view.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}

	deps := module.Dependencies{Config: cfg, Database: runtime, Renderer: renderer}
	repo := alerts.NewSQLRepository(runtime.SQL)
	h := alerts.NewHandlers(deps, repo, servers.NewSQLRepository(runtime.SQL), notifier)

	channel, err := repo.CreateChannel(context.Background(), alerts.Channel{
		Kind: alerts.ChannelKindTelegram, Name: "Ops", ChatID: "-100123", Enabled: true,
	})
	if err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /alerts/channels/{id}/test", h.ChannelTest)
	return mux, channel.ID
}

func postTest(mux *http.ServeMux, channelID int64) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/alerts/channels/"+strconv.FormatInt(channelID, 10)+"/test",
		strings.NewReader(url.Values{}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestChannelTestSendsSampleMessage(t *testing.T) {
	notifier := &fakeNotifier{}
	mux, channelID := newChannelTestMux(t, notifier)

	rec := postTest(mux, channelID)
	if rec.Code != http.StatusOK {
		t.Fatalf("channel test = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if notifier.calls != 1 {
		t.Fatalf("notifier.calls = %d, want 1", notifier.calls)
	}
	if notifier.lastChatID != "-100123" {
		t.Fatalf("lastChatID = %q, want -100123", notifier.lastChatID)
	}
	if !strings.Contains(notifier.lastText, "93%") {
		t.Fatalf("sample message missing sample value:\n%s", notifier.lastText)
	}
}

func TestChannelTestReportsSendFailure(t *testing.T) {
	notifier := &fakeNotifier{err: errors.New("telegram: sendMessage failed (status 400): chat not found")}
	mux, channelID := newChannelTestMux(t, notifier)

	rec := postTest(mux, channelID)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("channel test = %d, want 502\n%s", rec.Code, rec.Body.String())
	}
	if notifier.calls != 1 {
		t.Fatalf("notifier.calls = %d, want 1", notifier.calls)
	}
}

func TestChannelTestNotConfigured(t *testing.T) {
	// A nil notifier represents "no bot token configured".
	mux, channelID := newChannelTestMux(t, nil)

	rec := postTest(mux, channelID)
	if rec.Code != http.StatusOK {
		t.Fatalf("channel test (no token) = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not configured") {
		t.Fatalf("expected a 'not configured' notice in the rendered page")
	}
}
