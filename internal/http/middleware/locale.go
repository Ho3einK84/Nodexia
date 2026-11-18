package middleware

import (
	"net/http"

	"github.com/Ho3einK84/Nodexia/internal/i18n"
)

// Locale resolves the request's active language and stores a bound *Localizer
// in the request context for view.NewPageData to read. Resolution order is:
// the language cookie (an explicit choice), then Accept-Language on first
// visit, then the default language. Static, PWA, and health endpoints are
// skipped — they never render localized HTML.
func Locale(bundle *i18n.Bundle) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if shouldSkipSecurityMiddleware(r) {
				next.ServeHTTP(w, r)
				return
			}

			cookieValue := ""
			if cookie, err := r.Cookie(i18n.CookieName); err == nil {
				cookieValue = cookie.Value
			}

			code := bundle.Resolve(cookieValue, r.Header.Get("Accept-Language"))
			loc := bundle.Localizer(code)
			next.ServeHTTP(w, r.WithContext(i18n.NewContext(r.Context(), loc)))
		})
	}
}
