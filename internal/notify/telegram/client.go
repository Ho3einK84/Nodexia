// Package telegram implements notify.Notifier against the Telegram Bot API
// using only the standard library. The bot token is held at construction and is
// never logged or surfaced: every returned error is passed through token
// redaction first, because the request URL embeds the token.
package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://api.telegram.org"
	defaultTimeout = 10 * time.Second
	maxResponse    = 64 << 10 // cap how much of a response body we read
)

// Client sends messages through the Telegram Bot API sendMessage endpoint.
type Client struct {
	token      string
	baseURL    string
	httpClient *http.Client
	timeout    time.Duration
}

// Option customizes a Client. The base URL and HTTP client are exposed mainly so
// tests can point the client at an httptest.Server.
type Option func(*Client)

// WithBaseURL overrides the Telegram API base URL (no trailing slash).
func WithBaseURL(baseURL string) Option {
	return func(c *Client) {
		if trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/"); trimmed != "" {
			c.baseURL = trimmed
		}
	}
}

// WithHTTPClient overrides the HTTP client used for requests.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

// WithTimeout overrides the per-request timeout.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		if timeout > 0 {
			c.timeout = timeout
		}
	}
}

// NewClient builds a Telegram client bound to a bot token.
func NewClient(token string, opts ...Option) *Client {
	c := &Client{
		token:      strings.TrimSpace(token),
		baseURL:    defaultBaseURL,
		httpClient: &http.Client{},
		timeout:    defaultTimeout,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// telegramResponse is the envelope every Bot API method returns.
type telegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	ErrorCode   int    `json:"error_code"`
}

// Send delivers text to a Telegram chat. It returns a wrapped, token-redacted
// error on transport failure or a non-OK API response.
func (c *Client) Send(ctx context.Context, chatID, text string) error {
	if c.token == "" {
		return errors.New("telegram: bot token is not configured")
	}
	if strings.TrimSpace(chatID) == "" {
		return errors.New("telegram: chat id is required")
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", c.baseURL, c.token)
	form := url.Values{}
	form.Set("chat_id", chatID)
	form.Set("text", text)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return c.redact(fmt.Errorf("telegram: build request: %w", err))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return c.redact(fmt.Errorf("telegram: send: %w", err))
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponse))

	var parsed telegramResponse
	_ = json.Unmarshal(body, &parsed)

	if resp.StatusCode != http.StatusOK || !parsed.OK {
		description := strings.TrimSpace(parsed.Description)
		if description == "" {
			description = strings.TrimSpace(string(body))
		}
		if description == "" {
			description = http.StatusText(resp.StatusCode)
		}
		return c.redact(fmt.Errorf("telegram: sendMessage failed (status %d): %s", resp.StatusCode, description))
	}

	return nil
}

// redact removes the bot token from an error message so it can never leak into
// logs or the UI. The endpoint URL embeds the token, so transport errors would
// otherwise expose it.
func (c *Client) redact(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if c.token != "" {
		message = strings.ReplaceAll(message, c.token, "[REDACTED]")
	}
	return errors.New(message)
}
