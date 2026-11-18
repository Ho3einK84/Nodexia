package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/i18n"
)

// langProbe is a terminal handler that records the resolved language/direction
// from the request context so the locale middleware can be asserted.
func langProbe(seen *string, dir *string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if loc := i18n.FromContext(r.Context()); loc != nil {
			*seen = loc.Lang()
			*dir = loc.Dir()
		}
		w.WriteHeader(http.StatusOK)
	})
}

func TestLocaleMiddlewareResolvesAndStores(t *testing.T) {
	bundle, err := i18n.Default()
	if err != nil {
		t.Fatalf("i18n.Default: %v", err)
	}

	cases := []struct {
		name     string
		cookie   string
		accept   string
		wantLang string
		wantDir  string
	}{
		{"default english", "", "", "en", "ltr"},
		{"accept-language persian", "", "fa-IR,fa;q=0.9", "fa", "rtl"},
		{"cookie overrides accept", "fa", "en-US,en;q=0.9", "fa", "rtl"},
		{"invalid cookie falls back", "zz", "", "en", "ltr"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var lang, dir string
			handler := Locale(bundle)(langProbe(&lang, &dir))

			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.cookie != "" {
				r.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: tc.cookie})
			}
			if tc.accept != "" {
				r.Header.Set("Accept-Language", tc.accept)
			}
			handler.ServeHTTP(httptest.NewRecorder(), r)

			if lang != tc.wantLang {
				t.Errorf("lang = %q, want %q", lang, tc.wantLang)
			}
			if dir != tc.wantDir {
				t.Errorf("dir = %q, want %q", dir, tc.wantDir)
			}
		})
	}
}

func TestLocaleMiddlewareSkipsStatic(t *testing.T) {
	bundle, err := i18n.Default()
	if err != nil {
		t.Fatalf("i18n.Default: %v", err)
	}

	var localizerPresent bool
	handler := Locale(bundle)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		localizerPresent = i18n.FromContext(r.Context()) != nil
	}))
	r := httptest.NewRequest(http.MethodGet, "/static/style.css", nil)
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if localizerPresent {
		t.Error("locale middleware should skip /static and not attach a localizer")
	}
}
