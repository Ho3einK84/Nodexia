// Two HTTP cookies are used intentionally:
//
//   - nodexia_session (name configurable via NODEXIA_SESSION_COOKIE_NAME): an
//     HMAC-signed session ID issued to every visitor, including unauthenticated
//     ones. It allows the CSRF middleware to protect the login POST before any
//     login has occurred, and is the anchor for CSRF token derivation.
//
//   - nodexia_auth: an HMAC-signed token carrying the authenticated user
//     identity and expiry, set only after a successful login and cleared on
//     logout. Keeping identity separate from the session lets the CSRF session
//     survive an auth expiry without conflation.
//
// Both cookies are HttpOnly, SameSite=Lax, and signed with the same secret.
package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/config"
)

const authTokenKey contextKey = "auth_token"

type authToken struct {
	Username  string    `json:"username"`
	ExpiresAt time.Time `json:"expires_at"`
}

func RequireAuth(cfg config.Config) Middleware {
	secret := []byte(cfg.Security.SessionSecret)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if shouldSkipAuth(r) {
				next.ServeHTTP(w, r)
				return
			}

			cookie, err := r.Cookie("nodexia_auth")
			if err != nil {
				redirectLogin(w, r)
				return
			}

			token, err := parseAuthToken(cookie.Value, secret)
			if err != nil || token.Username != cfg.Security.AdminUsername || time.Now().After(token.ExpiresAt) {
				http.SetCookie(w, ClearAuthCookie(cfg.Security.SessionCookieSecure))
				redirectLogin(w, r)
				return
			}

			ctx := context.WithValue(r.Context(), authTokenKey, token.Username)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func shouldSkipAuth(r *http.Request) bool {
	switch {
	case strings.HasPrefix(r.URL.Path, "/static/"):
		return true
	case r.URL.Path == "/healthz", strings.HasPrefix(r.URL.Path, "/healthz/"):
		return true
	case r.URL.Path == "/login":
		return true
	default:
		return false
	}
}

func redirectLogin(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func GenerateAuthToken(username string, secret []byte, ttl time.Duration) (string, error) {
	token := authToken{
		Username:  username,
		ExpiresAt: time.Now().UTC().Add(ttl),
	}

	payload, err := json.Marshal(token)
	if err != nil {
		return "", fmt.Errorf("auth: marshal token: %w", err)
	}

	sig := hmacSign(payload, secret)
	return hex.EncodeToString(payload) + "." + hex.EncodeToString(sig), nil
}

func parseAuthToken(value string, secret []byte) (authToken, error) {
	dot := strings.LastIndexByte(value, '.')
	if dot < 0 {
		return authToken{}, errors.New("auth: invalid token format")
	}

	payloadHex := value[:dot]
	sigHex := value[dot+1:]

	payload, err := hex.DecodeString(payloadHex)
	if err != nil {
		return authToken{}, fmt.Errorf("auth: decode payload: %w", err)
	}

	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return authToken{}, fmt.Errorf("auth: decode signature: %w", err)
	}

	expectedSig := hmacSign(payload, secret)
	if !hmac.Equal(sig, expectedSig) {
		return authToken{}, errors.New("auth: invalid signature")
	}

	var token authToken
	if err := json.Unmarshal(payload, &token); err != nil {
		return authToken{}, fmt.Errorf("auth: unmarshal token: %w", err)
	}

	return token, nil
}

func hmacSign(data, secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}

func BuildAuthCookie(token string, ttl time.Duration, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     "nodexia_auth",
		Value:    token,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

func ClearAuthCookie(secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     "nodexia_auth",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

func IsAuthenticated(ctx context.Context) bool {
	_, ok := ctx.Value(authTokenKey).(string)
	return ok
}
