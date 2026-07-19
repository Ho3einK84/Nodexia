package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/config"
)

const (
	sessionIDKey  contextKey = "session_id"
	csrfTokenKey  contextKey = "csrf_token"
	csrfFormField            = "_csrf_token"
)

func SecurityHeaders() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if shouldSkipSecurityMiddleware(r) {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "same-origin")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; script-src 'self'; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			next.ServeHTTP(w, r)
		})
	}
}

func Session(cfg config.Config) Middleware {
	secret := []byte(cfg.Security.SessionSecret)
	cookieName := cfg.Security.SessionCookieName
	sessionTTL := cfg.Security.SessionTTL
	cookieSecure := cfg.Security.SessionCookieSecure

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if shouldSkipSecurityMiddleware(r) {
				next.ServeHTTP(w, r)
				return
			}

			sessionID, needsRefresh := validateOrCreateSession(r, secret, cookieName, sessionTTL)
			if sessionID == "" {
				http.Error(w, "session unavailable", http.StatusInternalServerError)
				return
			}
			if needsRefresh {
				http.SetCookie(w, buildSessionCookie(cookieName, sessionID, secret, sessionTTL, cookieSecure))
			}

			csrfToken := DeriveCSRFToken(sessionID, secret)
			ctx := r.Context()
			ctx = context.WithValue(ctx, sessionIDKey, sessionID)
			ctx = context.WithValue(ctx, csrfTokenKey, csrfToken)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// CSRF validates unsafe HTTP requests using two complementary checks:
//  1. Origin/Referer header must match the server host (rejects cross-origin forms).
//  2. The hidden form field "_csrf_token" must match the session-derived token
//     (synchronizer-token pattern; thwarts CSRF even when Origin is absent).
//
// Both checks must pass. GET/HEAD/OPTIONS are exempted.
func CSRF(cfg config.Config) Middleware {
	secret := []byte(cfg.Security.SessionSecret)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if shouldSkipSecurityMiddleware(r) || isSafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			sessionID := GetSessionID(r.Context())
			if sessionID == "" {
				http.Error(w, "csrf: missing session", http.StatusForbidden)
				return
			}

			if err := validateSameOriginRequest(r); err != nil {
				http.Error(w, "csrf: "+err.Error(), http.StatusForbidden)
				return
			}

			// ParseForm is idempotent; calling it here does not prevent handlers
			// from reading form values later.
			if err := r.ParseForm(); err != nil {
				http.Error(w, "csrf: cannot parse form", http.StatusBadRequest)
				return
			}

			submitted := r.FormValue(csrfFormField)
			expected := DeriveCSRFToken(sessionID, secret)
			if submitted == "" || !hmac.Equal([]byte(submitted), []byte(expected)) {
				http.Error(w, "csrf: invalid token", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// DeriveCSRFToken computes a deterministic CSRF token from the session ID and
// the shared secret using HMAC-SHA256. The "csrf:" prefix domain-separates
// this from other HMAC usages that share the same secret.
func DeriveCSRFToken(sessionID string, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("csrf:" + sessionID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func GetSessionID(ctx context.Context) string {
	if value, ok := ctx.Value(sessionIDKey).(string); ok {
		return value
	}
	return ""
}

// GetCSRFToken returns the CSRF token stored in the request context by the
// Session middleware. Handlers should embed this value in every HTML form via
// a hidden input named "_csrf_token".
func GetCSRFToken(ctx context.Context) string {
	if value, ok := ctx.Value(csrfTokenKey).(string); ok {
		return value
	}
	return ""
}

func shouldSkipSecurityMiddleware(r *http.Request) bool {
	switch {
	case strings.HasPrefix(r.URL.Path, "/static/"):
		return true
	case isPublicPWAAsset(r.URL.Path):
		return true
	case r.URL.Path == "/healthz", strings.HasPrefix(r.URL.Path, "/healthz/"):
		return true
	case r.URL.Path == "/metrics":
		// Token-gated in its own handler; scrapers carry no session cookies.
		return true
	default:
		return false
	}
}

// isPublicPWAAsset reports whether the path is a Progressive Web App entry point
// (manifest or service worker) that must be reachable without authentication and
// served with its own caching headers, like the /static assets.
func isPublicPWAAsset(path string) bool {
	return path == "/manifest.webmanifest" || path == "/sw.js"
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func validateOrCreateSession(r *http.Request, secret []byte, cookieName string, ttl time.Duration) (string, bool) {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		sessionID, createErr := randomSessionID()
		if createErr != nil {
			return "", true
		}
		return sessionID, true
	}

	sessionID, issuedAt, err := parseSignedSession(cookie.Value, secret)
	if err != nil {
		newID, createErr := randomSessionID()
		if createErr != nil {
			return "", true
		}
		return newID, true
	}

	if time.Since(issuedAt) > ttl {
		newID, createErr := randomSessionID()
		if createErr != nil {
			return "", true
		}
		return newID, true
	}

	if time.Until(issuedAt.Add(ttl)) < ttl/4 {
		return sessionID, true
	}

	return sessionID, false
}

func buildSessionCookie(name string, sessionID string, secret []byte, ttl time.Duration, secure bool) *http.Cookie {
	value := signSessionValue(sessionID, time.Now().UTC(), secret)
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

func randomSessionID() (string, error) {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func signSessionValue(sessionID string, issuedAt time.Time, secret []byte) string {
	issuedUnix := strconv.FormatInt(issuedAt.Unix(), 10)
	payload := sessionID + "." + issuedUnix
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + signature
}

func parseSignedSession(value string, secret []byte) (string, time.Time, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return "", time.Time{}, errors.New("invalid session format")
	}

	payload := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payload))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return "", time.Time{}, errors.New("invalid session signature")
	}

	issuedUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("invalid session timestamp: %w", err)
	}

	return parts[0], time.Unix(issuedUnix, 0).UTC(), nil
}

// ValidateSameOriginRequest is exported for WebSocket handlers that must perform
// their own same-origin check outside the CSRF middleware.
func ValidateSameOriginRequest(r *http.Request) error {
	return validateSameOriginRequest(r)
}

func validateSameOriginRequest(r *http.Request) error {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	referer := strings.TrimSpace(r.Header.Get("Referer"))

	var candidate string
	switch {
	case origin != "":
		candidate = origin
	case referer != "":
		candidate = referer
	default:
		return errors.New("origin or referer header is required")
	}

	parsed, err := url.Parse(candidate)
	if err != nil {
		return fmt.Errorf("invalid origin metadata: %w", err)
	}
	if !equalHost(parsed.Host, requestHost(r)) {
		return fmt.Errorf("cross-site request rejected for host %q", parsed.Host)
	}
	return nil
}

func requestHost(r *http.Request) string {
	return strings.TrimSpace(strings.ToLower(r.Host))
}

func equalHost(left, right string) bool {
	left = stripDefaultPort(strings.TrimSpace(strings.ToLower(left)))
	right = stripDefaultPort(strings.TrimSpace(strings.ToLower(right)))
	return left != "" && right != "" && left == right
}

// stripDefaultPort removes the default HTTP/HTTPS port suffix so that
// "example.com:443" matches "example.com" for same-origin checks.
func stripDefaultPort(host string) string {
	switch {
	case strings.HasSuffix(host, ":80"):
		return host[:len(host)-3]
	case strings.HasSuffix(host, ":443"):
		return host[:len(host)-4]
	default:
		return host
	}
}
