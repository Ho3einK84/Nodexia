package handlers

import (
	"crypto/subtle"
	"fmt"
	"math"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
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

	clientKey := h.clientKey(r)
	if allowed, retryAfter := h.throttle.Allowed(clientKey); !allowed {
		w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
		h.renderLogin(w, r, http.StatusTooManyRequests, "error", fmt.Sprintf(
			"Too many failed attempts. Try again in %s.", humanizeRetryAfter(retryAfter)))
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	expectedUser := strings.TrimSpace(h.config.Security.AdminUsername)
	expectedPass := h.config.Security.AdminPassword

	userMatch := subtle.ConstantTimeCompare([]byte(username), []byte(expectedUser)) == 1
	passMatch := subtle.ConstantTimeCompare([]byte(password), []byte(expectedPass)) == 1

	if !userMatch || !passMatch {
		if retryAfter := h.throttle.RecordFailure(clientKey); retryAfter > 0 {
			w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
			h.renderLogin(w, r, http.StatusTooManyRequests, "error", fmt.Sprintf(
				"Too many failed attempts. Try again in %s.", humanizeRetryAfter(retryAfter)))
			return
		}
		h.renderLogin(w, r, http.StatusUnauthorized, "error", "Invalid username or password.")
		return
	}

	h.throttle.Reset(clientKey)

	token, err := middleware.GenerateAuthToken(username, []byte(h.config.Security.SessionSecret), h.config.Security.SessionTTL)
	if err != nil {
		http.Error(w, "could not generate auth token", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, middleware.BuildAuthCookie(token, h.config.Security.SessionTTL, h.config.Security.SessionCookieSecure))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h LoginHandler) renderLogin(w http.ResponseWriter, r *http.Request, status int, flashKind, flashMessage string) {
	page := view.NewPageData(h.config, r)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = "Login - " + h.config.App.Name
	page.ContentTemplate = "content-login"
	page.PageTitle = "Administrator login"
	page.PageDescription = "Enter your credentials to access the control panel."
	page.FlashKind = flashKind
	page.FlashMessage = flashMessage

	if err := h.renderer.Render(w, status, page); err != nil {
		http.Error(w, "render login page", http.StatusInternalServerError)
	}
}

// clientKey identifies the caller for throttling. Behind a trusted reverse
// proxy the original client address is taken from the forwarding headers;
// otherwise only the direct connection address is trusted so callers cannot
// spoof their identity to dodge the limiter.
func (h LoginHandler) clientKey(r *http.Request) string {
	if h.config.Install.BehindReverseProxy {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			if first := strings.TrimSpace(strings.Split(forwarded, ",")[0]); first != "" {
				return first
			}
		}
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
			return realIP
		}
	}

	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
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
	cookieSecure bool
}

func NewLogoutHandler(cookieSecure bool) LogoutHandler {
	return LogoutHandler{cookieSecure: cookieSecure}
}

func (h LogoutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, middleware.ClearAuthCookie(h.cookieSecure))
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
