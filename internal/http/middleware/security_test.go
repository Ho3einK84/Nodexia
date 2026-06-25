package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
)

func testConfig() config.Config {
	return config.Config{
		Security: config.SecurityConfig{
			SessionCookieName:   "nodexia_session",
			SessionSecret:       "test-secret-key-for-unit-tests",
			SessionTTL:          12 * time.Hour,
			SessionCookieSecure: false,
		},
	}
}

// --- DeriveCSRFToken ---

func TestDeriveCSRFToken_Deterministic(t *testing.T) {
	secret := []byte("secret")
	tok1 := middleware.DeriveCSRFToken("session-abc", secret)
	tok2 := middleware.DeriveCSRFToken("session-abc", secret)
	if tok1 != tok2 {
		t.Fatalf("DeriveCSRFToken not deterministic: %q != %q", tok1, tok2)
	}
}

func TestDeriveCSRFToken_DifferentSessions(t *testing.T) {
	secret := []byte("secret")
	tok1 := middleware.DeriveCSRFToken("session-A", secret)
	tok2 := middleware.DeriveCSRFToken("session-B", secret)
	if tok1 == tok2 {
		t.Fatal("DeriveCSRFToken produced same token for different session IDs")
	}
}

func TestDeriveCSRFToken_DifferentSecrets(t *testing.T) {
	tok1 := middleware.DeriveCSRFToken("session-X", []byte("secret-1"))
	tok2 := middleware.DeriveCSRFToken("session-X", []byte("secret-2"))
	if tok1 == tok2 {
		t.Fatal("DeriveCSRFToken produced same token for different secrets")
	}
}

// --- Session middleware ---

func TestSession_IssuesCookieOnFirstVisit(t *testing.T) {
	cfg := testConfig()
	handler := middleware.Session(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == cfg.Security.SessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie to be set on first visit")
	}
	if sessionCookie.Value == "" {
		t.Fatal("session cookie value is empty")
	}
}

func TestSession_StoresSessionIDInContext(t *testing.T) {
	cfg := testConfig()
	var gotSessionID string
	handler := middleware.Session(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSessionID = middleware.GetSessionID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if gotSessionID == "" {
		t.Fatal("session ID should be stored in context")
	}
}

func TestSession_StoresCSRFTokenInContext(t *testing.T) {
	cfg := testConfig()
	var gotCSRF string
	handler := middleware.Session(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCSRF = middleware.GetCSRFToken(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if gotCSRF == "" {
		t.Fatal("CSRF token should be stored in context")
	}
}

func TestSession_SkipsStaticPaths(t *testing.T) {
	cfg := testConfig()
	handler := middleware.Session(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/static/app.js", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	for _, c := range rec.Result().Cookies() {
		if c.Name == cfg.Security.SessionCookieName {
			t.Fatal("session cookie should not be set for /static/ paths")
		}
	}
}

func TestCSRF_AllowsSafeMethods(t *testing.T) {
	cfg := testConfig()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := middleware.CSRF(cfg)(inner)

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		req := httptest.NewRequest(method, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s should be allowed without CSRF token, got %d", method, rec.Code)
		}
	}
}

func TestCSRF_BlocksMissingSession(t *testing.T) {
	cfg := testConfig()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := middleware.CSRF(cfg)(inner)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Host = "example.com"
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 when session is missing, got %d", rec.Code)
	}
}

func TestCSRF_BlocksCrossOrigin(t *testing.T) {
	cfg := testConfig()
	// Run through Session middleware first to get a real session in context.
	var csrfToken string
	var sessionCookieVal string
	setup := middleware.Session(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		csrfToken = middleware.GetCSRFToken(r.Context())
	}))
	rec0 := httptest.NewRecorder()
	setup.ServeHTTP(rec0, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, c := range rec0.Result().Cookies() {
		if c.Name == cfg.Security.SessionCookieName {
			sessionCookieVal = c.Value
		}
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := middleware.Session(cfg)(middleware.CSRF(cfg)(inner))

	form := url.Values{"_csrf_token": {csrfToken}}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://evil.example.com") // different host
	req.Host = "example.com"
	req.AddCookie(&http.Cookie{Name: cfg.Security.SessionCookieName, Value: sessionCookieVal})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for cross-origin request, got %d", rec.Code)
	}
}

func TestCSRF_AllowsValidSameOriginPost(t *testing.T) {
	cfg := testConfig()
	var csrfToken string
	var sessionCookieVal string

	// Capture session cookie and CSRF token.
	setup := middleware.Session(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		csrfToken = middleware.GetCSRFToken(r.Context())
	}))
	rec0 := httptest.NewRecorder()
	req0 := httptest.NewRequest(http.MethodGet, "/", nil)
	setup.ServeHTTP(rec0, req0)
	for _, c := range rec0.Result().Cookies() {
		if c.Name == cfg.Security.SessionCookieName {
			sessionCookieVal = c.Value
		}
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := middleware.Session(cfg)(middleware.CSRF(cfg)(inner))

	form := url.Values{"_csrf_token": {csrfToken}}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://example.com")
	req.Host = "example.com"
	req.AddCookie(&http.Cookie{Name: cfg.Security.SessionCookieName, Value: sessionCookieVal})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("valid same-origin POST with correct CSRF token should succeed, got %d", rec.Code)
	}
}

func TestCSRF_BlocksWrongToken(t *testing.T) {
	cfg := testConfig()
	var sessionCookieVal string

	setup := middleware.Session(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec0 := httptest.NewRecorder()
	setup.ServeHTTP(rec0, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, c := range rec0.Result().Cookies() {
		if c.Name == cfg.Security.SessionCookieName {
			sessionCookieVal = c.Value
		}
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := middleware.Session(cfg)(middleware.CSRF(cfg)(inner))

	form := url.Values{"_csrf_token": {"this-is-not-the-right-token"}}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://example.com")
	req.Host = "example.com"
	req.AddCookie(&http.Cookie{Name: cfg.Security.SessionCookieName, Value: sessionCookieVal})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("wrong CSRF token should be rejected with 403, got %d", rec.Code)
	}
}
