package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/ratelimit"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

func authTestConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		Version: "test",
		Security: config.SecurityConfig{
			AdminUsername:       "admin",
			AdminPassword:       "s3cret-passphrase",
			SessionSecret:       "test-session-secret-32-bytes-long",
			SessionCookieName:   "nodexia_session",
			SessionTTL:          time.Hour,
			SessionCookieSecure: false,
		},
	}
}

func TestLoginRotatesSessionCookieAndLogoutClearsAllCookies(t *testing.T) {
	cfg := authTestConfig(t)
	renderer, err := view.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	throttle := ratelimit.NewLoginThrottle(5, 30*time.Second, 15*time.Minute)
	loginHandler := NewLoginHandler(cfg, renderer, throttle)
	logoutHandler := NewLogoutHandler(cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", loginHandler.ServeHTTP)
	mux.HandleFunc("POST /login", loginHandler.ServeHTTP)
	mux.Handle("POST /logout", logoutHandler)
	mux.HandleFunc("POST /protected", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	wrapped := middleware.Session(cfg)(middleware.CSRF(cfg)(mux))

	// 1. Pre-login session + CSRF token.
	getReq := httptest.NewRequest(http.MethodGet, "/login", nil)
	getRec := httptest.NewRecorder()
	wrapped.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /login = %d", getRec.Code)
	}

	var preSession *http.Cookie
	for _, c := range getRec.Result().Cookies() {
		if c.Name == cfg.Security.SessionCookieName {
			preSession = c
		}
	}
	if preSession == nil {
		t.Fatal("expected pre-login session cookie")
	}

	// Extract pre-login CSRF token from hidden form input.
	body := getRec.Body.String()
	const marker = `name="_csrf_token" value="`
	idx := strings.Index(body, marker)
	if idx < 0 {
		t.Fatalf("CSRF token input not found in login HTML")
	}
	rest := body[idx+len(marker):]
	preCSRF := rest[:strings.IndexByte(rest, '"')]

	// 2. Authenticate: session fixation requires rotating nodexia_session.
	form := url.Values{
		"_csrf_token": {preCSRF},
		"username":    {cfg.Security.AdminUsername},
		"password":    {cfg.Security.AdminPassword},
	}
	postReq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("Origin", "http://example.com")
	postReq.Host = "example.com"
	postReq.AddCookie(preSession)

	postRec := httptest.NewRecorder()
	wrapped.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusSeeOther {
		t.Fatalf("POST /login status = %d, want 303", postRec.Code)
	}

	var postSession, authCookie *http.Cookie
	for _, c := range postRec.Result().Cookies() {
		switch c.Name {
		case cfg.Security.SessionCookieName:
			postSession = c
		case "nodexia_auth":
			authCookie = c
		}
	}
	if postSession == nil || authCookie == nil {
		t.Fatalf("expected rotated session cookie and auth cookie, got session=%v auth=%v", postSession, authCookie)
	}
	if postSession.Value == preSession.Value {
		t.Fatal("session fixation risk: nodexia_session was NOT rotated on login")
	}

	// Pre-login CSRF token must fail against the rotated post-login session.
	staleCSRFReq := httptest.NewRequest(http.MethodPost, "/protected", nil)
	staleCSRFReq.Header.Set("Origin", "http://example.com")
	staleCSRFReq.Header.Set("X-CSRF-Token", preCSRF)
	staleCSRFReq.Host = "example.com"
	staleCSRFReq.AddCookie(postSession)
	staleCSRFReq.AddCookie(authCookie)
	staleCSRFRec := httptest.NewRecorder()
	wrapped.ServeHTTP(staleCSRFRec, staleCSRFReq)
	if staleCSRFRec.Code != http.StatusForbidden {
		t.Errorf("pre-login CSRF token should fail with rotated session, got %d", staleCSRFRec.Code)
	}

	// 3. Logout clears both auth and session cookies.
	logoutRec := httptest.NewRecorder()
	logoutReq := httptest.NewRequest(http.MethodPost, "/logout", nil)
	logoutHandler.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusSeeOther {
		t.Fatalf("POST /logout status = %d, want 303", logoutRec.Code)
	}
	cleared := map[string]*http.Cookie{}
	for _, c := range logoutRec.Result().Cookies() {
		cleared[c.Name] = c
	}
	if c := cleared["nodexia_auth"]; c == nil || c.MaxAge >= 0 {
		t.Errorf("logout must expire nodexia_auth cookie, got %+v", c)
	}
	if c := cleared[cfg.Security.SessionCookieName]; c == nil || c.MaxAge >= 0 {
		t.Errorf("logout must expire %s cookie, got %+v", cfg.Security.SessionCookieName, c)
	}
}

func TestLoginRejectsMismatchedLengthsAndEmptyConfig(t *testing.T) {
	cfg := authTestConfig(t)
	renderer, err := view.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
	}
	throttle := ratelimit.NewLoginThrottle(5, 30*time.Second, 15*time.Minute)
	h := NewLoginHandler(cfg, renderer, throttle)

	// Different password length -> 401.
	form := url.Values{"username": {"admin"}, "password": {"short"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.10:4242"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}

	// Empty configured password must never authenticate even on empty submission.
	emptyCfg := authTestConfig(t)
	emptyCfg.Security.AdminPassword = ""
	eh := NewLoginHandler(emptyCfg, renderer, throttle)
	eForm := url.Values{"username": {"admin"}, "password": {""}}
	eReq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(eForm.Encode()))
	eReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	eReq.RemoteAddr = "203.0.113.11:4242"
	eRec := httptest.NewRecorder()
	eh.ServeHTTP(eRec, eReq)
	if eRec.Code != http.StatusUnauthorized {
		t.Errorf("empty configured password must fail closed (401), got %d", eRec.Code)
	}
}

func TestLoginClientKey_UsesRightmostValidForwardedIPOnlyWhenProxied(t *testing.T) {
	cfg := authTestConfig(t)
	renderer, _ := view.NewRenderer()
	throttle := ratelimit.NewLoginThrottle(5, 30*time.Second, 15*time.Minute)

	cfg.Install.BehindReverseProxy = false
	direct := NewLoginHandler(cfg, renderer, throttle)
	r1 := httptest.NewRequest(http.MethodPost, "/login", nil)
	r1.RemoteAddr = "198.51.100.5:12345"
	r1.Header.Set("X-Forwarded-For", "1.1.1.1, 2.2.2.2")
	if got := direct.clientKey(r1); got != "198.51.100.5" {
		t.Errorf("direct clientKey = %q, want 198.51.100.5", got)
	}

	cfg.Install.BehindReverseProxy = true
	proxied := NewLoginHandler(cfg, renderer, throttle)
	r2 := httptest.NewRequest(http.MethodPost, "/login", nil)
	r2.RemoteAddr = "127.0.0.1:4444"
	r2.Header.Set("X-Forwarded-For", "spoofed.invalid, 1.1.1.1, 203.0.113.77, not-an-ip")
	if got := proxied.clientKey(r2); got != "203.0.113.77" {
		t.Errorf("proxied clientKey = %q, want rightmost valid IP 203.0.113.77", got)
	}
}
