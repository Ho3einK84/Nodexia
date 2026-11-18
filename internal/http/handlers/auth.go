package handlers

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/i18n"
	"github.com/Ho3einK84/Nodexia/internal/ratelimit"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

type LoginHandler struct {
	config   config.Config
	renderer *view.Renderer
	throttle *ratelimit.LoginThrottle
}

func NewLoginHandler(cfg config.Config, renderer *view.Renderer, throttle *ratelimit.LoginThrottle) LoginHandler {
	return LoginHandler{config: cfg, renderer: renderer, throttle: throttle}
}

func (h LoginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if middleware.IsAuthenticated(r.Context()) {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		h.renderLogin(w, r, http.StatusOK, "", "")
	case http.MethodPost:
		h.handleLogin(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h LoginHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	loc := loginLocalizer(r)
	clientKey := h.clientKey(r)
	if allowed, retryAfter := h.throttle.Allowed(clientKey); !allowed {
		w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
		h.renderLogin(w, r, http.StatusTooManyRequests, "error",
			loc.T("login.too_many", "duration", humanizeRetryAfter(retryAfter)))
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	if len(username) > 1024 || len(password) > 1024 {
		h.renderLogin(w, r, http.StatusBadRequest, "error", loc.T("login.invalid"))
		return
	}

	expectedUser := strings.TrimSpace(h.config.Security.AdminUsername)
	expectedPass := h.config.Security.AdminPassword

	userMatch := expectedUser != "" && constantTimeHashEqual(username, expectedUser)
	passMatch := expectedPass != "" && constantTimeHashEqual(password, expectedPass)

	if !userMatch || !passMatch {
		if retryAfter := h.throttle.RecordFailure(clientKey); retryAfter > 0 {
			w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
			h.renderLogin(w, r, http.StatusTooManyRequests, "error",
				loc.T("login.too_many", "duration", humanizeRetryAfter(retryAfter)))
			return
		}
		h.renderLogin(w, r, http.StatusUnauthorized, "error", loc.T("login.invalid"))
		return
	}

	h.throttle.Reset(clientKey)

	token, err := middleware.GenerateAuthToken(username, []byte(h.config.Security.SessionSecret), h.config.Security.SessionTTL)
	if err != nil {
		http.Error(w, "could not generate auth token", http.StatusInternalServerError)
		return
	}

	// Rotate the pre-login session cookie to prevent session fixation and ensure
	// post-auth CSRF tokens bind to a brand-new session identifier.
	sessionCookie, _, err := middleware.RotateSessionCookie(h.config)
	if err != nil {
		http.Error(w, "could not rotate session", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, sessionCookie)
	http.SetCookie(w, middleware.BuildAuthCookie(token, h.config.Security.SessionTTL, h.config.Security.SessionCookieSecure))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func constantTimeHashEqual(left, right string) bool {
	leftSum := sha256.Sum256([]byte(left))
	rightSum := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftSum[:], rightSum[:]) == 1
}

func (h LoginHandler) renderLogin(w http.ResponseWriter, r *http.Request, status int, flashKind, flashMessage string) {
	page := view.NewPageData(h.config, r)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = page.T("login.title", "app", h.config.App.Name)
	page.ContentTemplate = "content-login"
	page.PageStyles = []string{"/static/login.css"}
	page.PageScripts = []string{"/static/login.js"}
	page.PageTitle = page.T("login.heading")
	page.PageDescription = page.T("login.subtitle")
	page.FlashKind = flashKind
	page.FlashMessage = flashMessage

	if err := h.renderer.Render(w, status, page); err != nil {
		http.Error(w, "render login page", http.StatusInternalServerError)
	}
}

// loginLocalizer returns the request's localizer, falling back to the default
// language so login messages render even before authentication.
func loginLocalizer(r *http.Request) *i18n.Localizer {
	if loc := i18n.FromContext(r.Context()); loc != nil {
		return loc
	}
	return i18n.MustDefault().Localizer(i18n.DefaultLanguage)
}

// clientKey identifies the caller for throttling. Behind a trusted reverse
// proxy the original client address is taken from the forwarding headers;
// otherwise only the direct connection address is trusted so callers cannot
// spoof their identity to dodge the limiter.
func (h LoginHandler) clientKey(r *http.Request) string {
	if h.config.Install.BehindReverseProxy {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			// Scan right-to-left so client-forged leading XFF values cannot bypass
			// per-IP rate limiting when the edge proxy appends the peer address.
			parts := strings.Split(forwarded, ",")
			for i := len(parts) - 1; i >= 0; i-- {
				if addr, err := netip.ParseAddr(strings.TrimSpace(parts[i])); err == nil {
					return addr.String()
				}
			}
		}
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
			if addr, err := netip.ParseAddr(realIP); err == nil {
				return addr.String()
			}
		}
	}

	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		if addr, parseErr := netip.ParseAddr(host); parseErr == nil {
			return addr.String()
		}
		return host
	}
	if addr, err := netip.ParseAddr(strings.TrimSpace(r.RemoteAddr)); err == nil {
		return addr.String()
	}
	return r.RemoteAddr
}

func retryAfterSeconds(d time.Duration) string {
	seconds := int(math.Ceil(d.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("%d", seconds)
}

func humanizeRetryAfter(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return d.String()
	}
	return d.Truncate(time.Second).String()
}

type LogoutHandler struct {
	config config.Config
}

func NewLogoutHandler(cfg config.Config) LogoutHandler {
	return LogoutHandler{config: cfg}
}

func (h LogoutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	http.SetCookie(w, middleware.ClearAuthCookie(h.config.Security.SessionCookieSecure))
	http.SetCookie(w, middleware.ClearSessionCookie(h.config))
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
