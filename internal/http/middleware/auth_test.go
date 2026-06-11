package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
)

func authTestConfig(username string) config.Config {
	return config.Config{
		Security: config.SecurityConfig{
			SessionCookieName:   "nodexia_session",
			SessionSecret:       "test-secret-auth-unit-tests-key",
			SessionTTL:          12 * time.Hour,
			SessionCookieSecure: false,
			AdminUsername:       username,
		},
	}
}

// --- GenerateAuthToken / parseAuthToken roundtrip (via BuildAuthCookie) ---

func TestGenerateAuthToken_Roundtrip(t *testing.T) {
	secret := []byte("roundtrip-test-secret")
	token, err := middleware.GenerateAuthToken("alice", secret, time.Hour)
	if err != nil {
		t.Fatalf("GenerateAuthToken error: %v", err)
	}
	if token == "" {
		t.Fatal("GenerateAuthToken returned empty token")
	}
}

// --- BuildAuthCookie ---

func TestBuildAuthCookie_Fields(t *testing.T) {
	c := middleware.BuildAuthCookie("tok123", time.Hour, false)
	if c.Name != "nodexia_auth" {
		t.Errorf("cookie name = %q, want nodexia_auth", c.Name)
	}
	if c.Value != "tok123" {
		t.Errorf("cookie value = %q, want tok123", c.Value)
	}
	if !c.HttpOnly {
		t.Error("cookie should be HttpOnly")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Error("cookie SameSite should be Lax")
	}
	if c.MaxAge != int(time.Hour.Seconds()) {
		t.Errorf("cookie MaxAge = %d, want %d", c.MaxAge, int(time.Hour.Seconds()))
	}
}

// --- ClearAuthCookie ---

func TestClearAuthCookie_Fields(t *testing.T) {
	c := middleware.ClearAuthCookie(false)
	if c.Name != "nodexia_auth" {
		t.Errorf("cookie name = %q, want nodexia_auth", c.Name)
	}
	if c.MaxAge != -1 {
		t.Errorf("clear cookie MaxAge = %d, want -1", c.MaxAge)
	}
	if c.Value != "" {
		t.Errorf("clear cookie value = %q, want empty", c.Value)
	}
}

// --- RequireAuth ---

func TestRequireAuth_RedirectsUnauthenticated(t *testing.T) {
	cfg := authTestConfig("admin")
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := middleware.RequireAuth(cfg)(inner)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("unauthenticated request should redirect, got %d", rec.Code)
	}
	if rec.Header().Get("Location") != "/login" {
		t.Errorf("redirect location = %q, want /login", rec.Header().Get("Location"))
	}
}

func TestRequireAuth_PassesAuthenticatedRequest(t *testing.T) {
	cfg := authTestConfig("admin")
	secret := []byte(cfg.Security.SessionSecret)

	token, err := middleware.GenerateAuthToken("admin", secret, time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := middleware.RequireAuth(cfg)(inner)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(middleware.BuildAuthCookie(token, time.Hour, false))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("authenticated request should pass, got %d", rec.Code)
	}
}

func TestRequireAuth_RejectsWrongUsername(t *testing.T) {
	cfg := authTestConfig("admin")
	secret := []byte(cfg.Security.SessionSecret)

	// Token for a different username.
	token, err := middleware.GenerateAuthToken("hacker", secret, time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := middleware.RequireAuth(cfg)(inner)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(middleware.BuildAuthCookie(token, time.Hour, false))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("wrong-username token should redirect, got %d", rec.Code)
	}
}

func TestRequireAuth_RejectsExpiredToken(t *testing.T) {
	cfg := authTestConfig("admin")
	secret := []byte(cfg.Security.SessionSecret)

	// Token with -1 second TTL (already expired).
	token, err := middleware.GenerateAuthToken("admin", secret, -time.Second)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := middleware.RequireAuth(cfg)(inner)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(middleware.BuildAuthCookie(token, time.Hour, false))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expired token should redirect, got %d", rec.Code)
	}
}

func TestRequireAuth_SkipsLoginPage(t *testing.T) {
	cfg := authTestConfig("admin")
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := middleware.RequireAuth(cfg)(inner)

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("/login should skip auth check, got %d", rec.Code)
	}
}
