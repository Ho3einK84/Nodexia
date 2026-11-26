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
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/config"
)

const (
	sessionIDKey         contextKey = "session_id"
	csrfTokenKey         contextKey = "csrf_token"
	csrfFormField                   = "_csrf_token"
	csrfHeaderName                  = "X-CSRF-Token"
	maxCSRFFormBodyBytes            = 10 << 20 // 10 MiB
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
			w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), interest-cohort=()")
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
			w.Header().Set(csrfHeaderName, csrfToken)
			ctx := r.Context()
			ctx = context.WithValue(ctx, sessionIDKey, sessionID)
			ctx = context.WithValue(ctx, csrfTokenKey, csrfToken)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// CSRF validates unsafe HTTP requests using two complementary checks:
//  1. Origin/Referer header must match the server scheme and host (rejects
//     cross-origin and cross-scheme forms).
//  2. The synchronizer token must match the session-derived token via either
//     the X-CSRF-Token header, urlencoded form field, or multipart form field
//     "_csrf_token" (never URL query strings).
//
// Both checks must pass. GET/HEAD/OPTIONS are exempted.
func CSRF(cfg config.Config) Middleware {
	secret := []byte(cfg.Security.SessionSecret)
	behindProxy := cfg.Install.BehindReverseProxy

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

			if err := validateSameOriginWithProxy(r, behindProxy); err != nil {
				http.Error(w, "csrf: "+err.Error(), http.StatusForbidden)
				return
			}

			submitted := strings.TrimSpace(r.Header.Get(csrfHeaderName))
			if submitted == "" {
				mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if mediaType == "multipart/form-data" {
					r.Body = http.MaxBytesReader(w, r.Body, maxCSRFFormBodyBytes)
					if err := r.ParseMultipartForm(maxCSRFFormBodyBytes); err != nil {
						var maxErr *http.MaxBytesError
						if errors.As(err, &maxErr) {
							http.Error(w, "csrf: request body too large", http.StatusRequestEntityTooLarge)
							return
						}
						http.Error(w, "csrf: cannot parse multipart form", http.StatusBadRequest)
						return
					}
					if r.MultipartForm != nil {
						if vals := r.MultipartForm.Value[csrfFormField]; len(vals) > 0 {
							submitted = strings.TrimSpace(vals[0])
						}
					}
				} else {
					// ParseForm is idempotent; calling it here does not prevent handlers
					// from reading form values later. Read exclusively from PostForm
					// (body fields), never r.FormValue which includes URL query strings.
					if err := r.ParseForm(); err != nil {
						http.Error(w, "csrf: cannot parse form", http.StatusBadRequest)
						return
					}
					submitted = strings.TrimSpace(r.PostFormValue(csrfFormField))
				}
			}

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

// RotateSessionCookie issues a fresh signed session cookie backed by a new
// random session ID so pre-authentication session fixation tokens cannot
// persist past login.
func RotateSessionCookie(cfg config.Config) (*http.Cookie, string, error) {
	sessionID, err := randomSessionID()
	if err != nil {
		return nil, "", err
	}
	cookie := buildSessionCookie(
		cfg.Security.SessionCookieName,
		sessionID,
		[]byte(cfg.Security.SessionSecret),
		cfg.Security.SessionTTL,
		cfg.Security.SessionCookieSecure,
	)
	return cookie, sessionID, nil
}

// ClearSessionCookie builds an expired cookie matching the configured session
// cookie attributes so browsers drop the session identifier on logout.
func ClearSessionCookie(cfg config.Config) *http.Cookie {
	return &http.Cookie{
		Name:     cfg.Security.SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
		HttpOnly: true,
		Secure:   cfg.Security.SessionCookieSecure,
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

// ValidateSameOriginRequest is exported for WebSocket handlers and unit tests
// without reverse-proxy header trust.
func ValidateSameOriginRequest(r *http.Request) error {
	return validateSameOriginWithProxy(r, false)
}

// ValidateSameOriginWithProxy validates Origin/Referer scheme and host against
// the incoming request, honoring X-Forwarded-Proto only when behindProxy is true.
func ValidateSameOriginWithProxy(r *http.Request, behindProxy bool) error {
	return validateSameOriginWithProxy(r, behindProxy)
}

func validateSameOriginWithProxy(r *http.Request, behindProxy bool) error {
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
	if parsed.User != nil {
		return errors.New("cross-site request rejected: userinfo not allowed")
	}

	parsedScheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if parsedScheme != "http" && parsedScheme != "https" {
		return fmt.Errorf("cross-site request rejected for scheme %q", parsed.Scheme)
	}

	expectedScheme := expectedRequestScheme(r, behindProxy)
	if parsedScheme != expectedScheme {
		return fmt.Errorf("cross-scheme request rejected: got %q, want %q", parsedScheme, expectedScheme)
	}

	if !equalHostForScheme(parsed.Host, requestHost(r), expectedScheme) {
		return fmt.Errorf("cross-site request rejected for host %q", parsed.Host)
	}
	return nil
}

func expectedRequestScheme(r *http.Request, behindProxy bool) string {
	if r.TLS != nil {
		return "https"
	}
	if behindProxy {
		if proto := forwardedProto(r); proto != "" {
			return proto
		}
	}
	if scheme := strings.ToLower(strings.TrimSpace(r.URL.Scheme)); scheme == "http" || scheme == "https" {
		return scheme
	}
	return "http"
}

func forwardedProto(r *http.Request) string {
	raw := r.Header.Get("X-Forwarded-Proto")
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.ToLower(strings.TrimSpace(parts[i]))
		if candidate == "http" || candidate == "https" {
			return candidate
		}
	}
	return ""
}

func requestHost(r *http.Request) string {
	return strings.TrimSpace(strings.ToLower(r.Host))
}

func equalHostForScheme(left, right, scheme string) bool {
	normLeft, okLeft := normalizeHostPort(left, scheme)
	normRight, okRight := normalizeHostPort(right, scheme)
	return okLeft && okRight && normLeft == normRight
}

func normalizeHostPort(rawHost, scheme string) (string, bool) {
	rawHost = strings.TrimSpace(strings.ToLower(rawHost))
	if rawHost == "" || strings.ContainsAny(rawHost, "@/?\\ \t\r\n") {
		return "", false
	}
	host, port, err := net.SplitHostPort(rawHost)
	if err != nil {
		if strings.HasPrefix(rawHost, "[") && strings.HasSuffix(rawHost, "]") {
			inner := strings.TrimSuffix(strings.TrimPrefix(rawHost, "["), "]")
			ip, parseErr := netip.ParseAddr(inner)
			if parseErr != nil {
				return "", false
			}
			return ip.String(), true
		}
		if strings.Contains(rawHost, ":") {
			ip, parseErr := netip.ParseAddr(rawHost)
			if parseErr != nil {
				return "", false
			}
			return ip.String(), true
		}
		return rawHost, true
	}
	if host == "" {
		return "", false
	}
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
		host = ip.String()
	}
	if port == "" {
		return host, true
	}
	return net.JoinHostPort(host, port), true
}
