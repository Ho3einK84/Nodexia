package i18n

import "testing"

// TestCatalogKeyParity fails if the English and Persian catalogs do not contain
// exactly the same set of keys. Key parity is what makes the fallback chain
// trustworthy: every fa key has an en counterpart and vice versa, so no string
// silently renders in the wrong language or as a raw key. New locales added in
// the future should be wired into this check too.
func TestCatalogKeyParity(t *testing.T) {
	b := mustBundle(t)

	reference := b.catalogs[DefaultLanguage]
	if reference == nil {
		t.Fatalf("default catalog %q is missing", DefaultLanguage)
	}

	for code, catalog := range b.catalogs {
		if code == DefaultLanguage {
			continue
		}

		for key := range reference {
			if _, ok := catalog[key]; !ok {
				t.Errorf("key %q present in %q but missing in %q", key, DefaultLanguage, code)
			}
		}
		for key := range catalog {
			if _, ok := reference[key]; !ok {
				t.Errorf("key %q present in %q but missing in %q", key, code, DefaultLanguage)
			}
		}

		// Plural keys must expose the same set of CLDR categories in every
		// language so Tn never falls through to the raw key.
		for key, refMsg := range reference {
			locMsg, ok := catalog[key]
			if !ok {
				continue
			}
			if len(refMsg.forms) > 1 || len(locMsg.forms) > 1 {
				for cat := range refMsg.forms {
					if _, ok := locMsg.forms[cat]; !ok {
						t.Errorf("plural key %q missing category %q in %q", key, cat, code)
					}
				}
			}
		}
	}
}
