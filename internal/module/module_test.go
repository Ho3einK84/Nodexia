package module

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/config"
	"github.com/Ho3einK84/Nodexia/internal/i18n"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

// TestPlaceholderHandlerLocalizesTitle verifies the module placeholder resolves
// its title/description KEYS against the request's active localizer, so the
// fallback page renders in the user's language rather than a baked-in English
// literal. The title key is static on the module; the resolution happens per
// request inside the handler, which is the seam we localize at.
func TestPlaceholderHandlerLocalizesTitle(t *testing.T) {
	renderer, err := view.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	deps := Dependencies{
		Config:   config.Config{App: config.AppConfig{Name: "Nodexia"}},
		Renderer: renderer,
	}
	handler := NewPlaceholderHandler(deps, PlaceholderPage{
		TitleKey:       "terminal.title",
		RouteGroup:     "/servers/{id}/terminal",
		DescriptionKey: "module.placeholder.terminal",
	})

	bundle := i18n.MustDefault()
	cases := map[string]struct{ want, absent string }{
		"en": {want: "Terminal", absent: "ترمینال"},
		"fa": {want: "ترمینال", absent: "Terminal"},
	}
	for lang, want := range cases {
		t.Run(lang, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/servers/1/terminal", nil)
			req = req.WithContext(i18n.NewContext(req.Context(), bundle.Localizer(lang)))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			body := rec.Body.String()
			if !strings.Contains(body, want.want) {
				t.Errorf("body missing localized title %q", want.want)
			}
			if want.absent != "" && strings.Contains(body, "<title>"+want.absent) {
				t.Errorf("body unexpectedly contains %q in title", want.absent)
			}
		})
	}
}
