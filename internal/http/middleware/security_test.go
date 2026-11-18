package middleware_test

import (
	"bytes"
	"crypto/tls"
	"mime/multipart"
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

func TestCSRF_AllowsHeaderTokenWithoutFormField(t *testing.T) {
	cfg := testConfig()
	var sessionCookieVal, csrfToken string

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

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := middleware.Session(cfg)(middleware.CSRF(cfg)(inner))

	req := httptest.NewRequest(http.MethodPost, "/ops/backup/inspect", strings.NewReader("payload=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", csrfToken)
	req.Header.Set("Origin", "http://example.com")
	req.Host = "example.com"
	req.AddCookie(&http.Cookie{Name: cfg.Security.SessionCookieName, Value: sessionCookieVal})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("X-CSRF-Token header should authorize POST without form field, got %d", rec.Code)
	}
}

func TestCSRF_MultipartFormFieldWithoutQueryToken(t *testing.T) {
	cfg := testConfig()
	var sessionCookieVal, csrfToken string

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

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, _, err := r.FormFile("file"); err != nil {
			http.Error(w, "missing file", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler := middleware.Session(cfg)(middleware.CSRF(cfg)(inner))

	buildMultipart := func(tok string) (*bytes.Buffer, string) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("_csrf_token", tok)
		fw, _ := mw.CreateFormFile("file", "backup.json")
		_, _ = fw.Write([]byte(`{}`))
		_ = mw.Close()
		return &buf, mw.FormDataContentType()
	}

	buf, ct := buildMultipart(csrfToken)
	req := httptest.NewRequest(http.MethodPost, "/ops/backup/import", buf)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Origin", "http://example.com")
	req.Host = "example.com"
	req.AddCookie(&http.Cookie{Name: cfg.Security.SessionCookieName, Value: sessionCookieVal})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("multipart _csrf_token without query string should succeed, got %d (%s)", rec.Code, rec.Body.String())
	}

	badBuf, badCT := buildMultipart("wrong-token")
	badReq := httptest.NewRequest(http.MethodPost, "/ops/backup/import", badBuf)
	badReq.Header.Set("Content-Type", badCT)
	badReq.Header.Set("Origin", "http://example.com")
	badReq.Host = "example.com"
	badReq.AddCookie(&http.Cookie{Name: cfg.Security.SessionCookieName, Value: sessionCookieVal})

	badRec := httptest.NewRecorder()
	handler.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusForbidden {
		t.Errorf("invalid multipart CSRF token should be rejected with 403, got %d", badRec.Code)
	}
}

func TestSecurityHeaders_IncludesPermissionsPolicyAndNoStore(t *testing.T) {
	handler := middleware.SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Permissions-Policy"); !strings.Contains(got, "camera=()") || !strings.Contains(got, "microphone=()") {
		t.Errorf("Permissions-Policy = %q, expected camera/microphone restrictions", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "script-src 'self'") {
		t.Errorf("CSP missing strict script-src 'self': %q", got)
	}
}

func TestCSRF_RejectsQueryStringTokenAndAcceptsHeader(t *testing.T) {
	cfg := testConfig()
	var sessionCookieVal, csrfToken string

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

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := middleware.Session(cfg)(middleware.CSRF(cfg)(okHandler))

	// Query-string-only CSRF token must be rejected so tokens never leak via URLs/logs.
	qReq := httptest.NewRequest(http.MethodPost, "/ops/backup/import?_csrf_token="+url.QueryEscape(csrfToken), nil)
	qReq.Header.Set("Origin", "http://example.com")
	qReq.Host = "example.com"
	qReq.AddCookie(&http.Cookie{Name: cfg.Security.SessionCookieName, Value: sessionCookieVal})
	qRec := httptest.NewRecorder()
	handler.ServeHTTP(qRec, qReq)
	if qRec.Code != http.StatusForbidden {
		t.Errorf("query-only CSRF token should return 403, got %d", qRec.Code)
	}

	// X-CSRF-Token header without query parameter must succeed.
	hReq := httptest.NewRequest(http.MethodPost, "/ops/backup/import", nil)
	hReq.Header.Set("Origin", "http://example.com")
	hReq.Header.Set("X-CSRF-Token", csrfToken)
	hReq.Host = "example.com"
	hReq.AddCookie(&http.Cookie{Name: cfg.Security.SessionCookieName, Value: sessionCookieVal})
	hRec := httptest.NewRecorder()
	handler.ServeHTTP(hRec, hReq)
	if hRec.Code != http.StatusNoContent {
		t.Errorf("header X-CSRF-Token should return 204, got %d (%s)", hRec.Code, hRec.Body.String())
	}
}

func TestValidateSameOriginWithProxy_SchemesIPv6AndUserinfo(t *testing.T) {
	t.Run("rejects https origin against plain http request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/submit", nil)
		req.Host = "example.com"
		req.Header.Set("Origin", "https://example.com")
		if err := middleware.ValidateSameOriginWithProxy(req, false); err == nil {
			t.Fatal("expected cross-scheme rejection")
		}
	})

	t.Run("rejects spoofed X-Forwarded-Proto when BehindReverseProxy is false", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/submit", nil)
		req.Host = "example.com"
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("Origin", "https://example.com")
		if err := middleware.ValidateSameOriginWithProxy(req, false); err == nil {
			t.Fatal("expected rejection when proxy trust is disabled")
		}
	})

	t.Run("accepts https origin behind proxy with X-Forwarded-Proto", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/submit", nil)
		req.Host = "example.com:443"
		req.Header.Set("X-Forwarded-Proto", "http, https")
		req.Header.Set("Origin", "https://example.com")
		if err := middleware.ValidateSameOriginWithProxy(req, true); err != nil {
			t.Fatalf("unexpected error behind proxy: %v", err)
		}
	})

	t.Run("accepts direct TLS request and rejects wrong port", func(t *testing.T) {
		okReq := httptest.NewRequest(http.MethodPost, "/submit", nil)
		okReq.TLS = &tls.ConnectionState{}
		okReq.Host = "example.com"
		okReq.Header.Set("Origin", "https://example.com:443")
		if err := middleware.ValidateSameOriginWithProxy(okReq, false); err != nil {
			t.Fatalf("TLS default port :443 should match: %v", err)
		}

		badReq := httptest.NewRequest(http.MethodPost, "/submit", nil)
		badReq.TLS = &tls.ConnectionState{}
		badReq.Host = "example.com"
		badReq.Header.Set("Origin", "https://example.com:80")
		if err := middleware.ValidateSameOriginWithProxy(badReq, false); err == nil {
			t.Fatal("expected port :80 on https to be rejected")
		}
	})

	t.Run("handles IPv6 brackets and rejects userinfo", func(t *testing.T) {
		ipv6Req := httptest.NewRequest(http.MethodPost, "/submit", nil)
		ipv6Req.Host = "[::1]"
		ipv6Req.Header.Set("Origin", "http://[::1]:80")
		if err := middleware.ValidateSameOriginWithProxy(ipv6Req, false); err != nil {
			t.Fatalf("IPv6 default port normalization failed: %v", err)
		}

		userReq := httptest.NewRequest(http.MethodPost, "/submit", nil)
		userReq.Host = "example.com"
		userReq.Header.Set("Origin", "http://admin@example.com")
		if err := middleware.ValidateSameOriginWithProxy(userReq, false); err == nil {
			t.Fatal("expected origin userinfo to be rejected")
		}
	})
}

func TestRotateAndClearSessionCookie(t *testing.T) {
	cfg := testConfig()
	c1, id1, err := middleware.RotateSessionCookie(cfg)
	if err != nil {
		t.Fatalf("RotateSessionCookie(1) error = %v", err)
	}
	c2, id2, err := middleware.RotateSessionCookie(cfg)
	if err != nil {
		t.Fatalf("RotateSessionCookie(2) error = %v", err)
	}
	if id1 == "" || id2 == "" || id1 == id2 || c1.Value == c2.Value {
		t.Fatalf("expected unique non-empty rotated sessions, got id1=%q id2=%q", id1, id2)
	}
	if !c1.HttpOnly || c1.SameSite != http.SameSiteLaxMode {
		t.Errorf("unexpected cookie security flags: HttpOnly=%v SameSite=%v", c1.HttpOnly, c1.SameSite)
	}

	cleared := middleware.ClearSessionCookie(cfg)
	if cleared.Name != cfg.Security.SessionCookieName || cleared.MaxAge != -1 || cleared.Value != "" {
		t.Errorf("ClearSessionCookie unexpected: %+v", cleared)
	}
}
