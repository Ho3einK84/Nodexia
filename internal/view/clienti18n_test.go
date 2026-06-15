package view

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/i18n"
)

// TestClientI18nKeysPresent guards that every key the JS layer expects exists in
// both catalogs: ClientMessages skips keys absent from every catalog, so a count
// shortfall means a client key was renamed or never added. This is the client
// counterpart to the catalog parity test.
func TestClientI18nKeysPresent(t *testing.T) {
	bundle := i18n.MustDefault()
	for _, lang := range []string{"en", "fa"} {
		loc := bundle.Localizer(lang)
		messages := loc.ClientMessages(clientI18nKeys)
		if len(messages) != len(clientI18nKeys) {
			for _, key := range clientI18nKeys {
				if _, ok := messages[key]; !ok {
					t.Errorf("%s: client i18n key %q missing from catalog", lang, key)
				}
			}
		}
	}
}

// TestClientI18nJSONValid checks the island serialises to valid JSON containing
// the expected keys and no executable "</script>" sequence (CSP/island safety).
func TestClientI18nJSONValid(t *testing.T) {
	loc := i18n.MustDefault().Localizer("fa")
	raw := string(clientI18nJSON(loc))

	if strings.Contains(strings.ToLower(raw), "</script") {
		t.Fatalf("client i18n JSON must not contain a closing script tag: %q", raw)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("client i18n JSON does not parse: %v", err)
	}

	// Spot-check a singular and a plural key resolve to the expected shapes.
	if _, ok := decoded["js.flash.dismiss"].(string); !ok {
		t.Errorf("expected js.flash.dismiss to be a string, got %T", decoded["js.flash.dismiss"])
	}
	if forms, ok := decoded["js.bulk.server_count"].(map[string]any); !ok {
		t.Errorf("expected js.bulk.server_count to be a plural object, got %T", decoded["js.bulk.server_count"])
	} else if _, ok := forms["other"]; !ok {
		t.Errorf("plural key js.bulk.server_count missing \"other\" form")
	}
}
