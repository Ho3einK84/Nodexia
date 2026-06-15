// Package i18n is Nodexia's lightweight, dependency-free internationalization
// layer. Translations live in per-language JSON files embedded at build time
// (see locales/*.json); each file is a flat map of dotted keys
// (e.g. "nav.servers") to either a plain string or a plural object with CLDR
// category forms ("one"/"other"). A Bundle holds every loaded language; a
// Localizer binds a Bundle to one active language and resolves keys, falling
// back to the default language and finally to the key itself when a string is
// missing so the UI never renders blank.
//
// The design deliberately avoids golang.org/x/text/message and
// nicksnyder/go-i18n: both add a dependency and a heavier catalog workflow,
// whereas a flat key→string map with simple per-language plural rules satisfies
// every requirement (pluralization, per-language files, drop-in new locales)
// while honouring the project's "few dependencies" constraint. Adding a
// language is just dropping a new <code>.json into locales/ — no code change.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"sort"
	"strings"
	"sync"
)

//go:embed locales/*.json
var localeFS embed.FS

// DefaultLanguage is the fallback locale: English. A key missing from the
// active language is looked up here before falling back to the raw key, and an
// unrecognised request language resolves to this.
const DefaultLanguage = "en"

// Direction is the writing direction of a language, mirrored onto the HTML
// <html dir> attribute and the PWA manifest.
type Direction string

const (
	LTR Direction = "ltr"
	RTL Direction = "rtl"
)

// Language describes one supported locale. NativeName is shown in the language
// switcher (e.g. "فارسی") so users recognise their own language regardless of
// the currently active one.
type Language struct {
	Code       string
	Name       string // English name, e.g. "Persian"
	NativeName string // endonym shown in the switcher, e.g. "فارسی"
	Dir        Direction
}

// rtlLanguages is the set of language codes that render right-to-left. New RTL
// locales (Arabic, Hebrew, Urdu, …) only need an entry here plus a JSON file.
var rtlLanguages = map[string]bool{
	"fa": true,
	"ar": true,
	"he": true,
	"ur": true,
}

// nativeNames maps a code to its endonym and English name for the switcher.
// Codes without an entry fall back to the bare code so the switcher still works.
var languageMeta = map[string]Language{
	"en": {Code: "en", Name: "English", NativeName: "English", Dir: LTR},
	"fa": {Code: "fa", Name: "Persian", NativeName: "فارسی", Dir: RTL},
}

// message is one entry in a locale file: either a single string (Other) or a
// set of CLDR plural-category forms keyed by "zero"/"one"/"two"/"few"/"many"/
// "other".
type message struct {
	forms map[string]string
}

func (m message) form(category string) (string, bool) {
	if v, ok := m.forms[category]; ok {
		return v, true
	}
	if v, ok := m.forms["other"]; ok {
		return v, true
	}
	return "", false
}

// Bundle holds the loaded catalogs for every language plus the ordered list of
// supported languages used to render the switcher.
type Bundle struct {
	catalogs  map[string]map[string]message
	languages []Language
}

var (
	defaultBundle *Bundle
	defaultOnce   sync.Once
	defaultErr    error
)

// Default returns the process-wide Bundle loaded once from the embedded locale
// files. Both the renderer and the locale middleware share it so locales are
// parsed a single time at startup.
func Default() (*Bundle, error) {
	defaultOnce.Do(func() {
		defaultBundle, defaultErr = Load(localeFS, "locales")
	})
	return defaultBundle, defaultErr
}

// MustDefault is Default but panics on error; only the program entrypoint and
// tests, which cannot proceed without translations, should use it.
func MustDefault() *Bundle {
	b, err := Default()
	if err != nil {
		panic("i18n: load default bundle: " + err.Error())
	}
	return b
}

// Load parses every <code>.json under dir in fsys into a Bundle. It is exported
// so tests can load fixture catalogs from a different filesystem.
func Load(fsys fs.FS, dir string) (*Bundle, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("i18n: read locales dir: %w", err)
	}

	bundle := &Bundle{catalogs: map[string]map[string]message{}}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		code := strings.TrimSuffix(name, ".json")
		raw, err := fs.ReadFile(fsys, dir+"/"+name)
		if err != nil {
			return nil, fmt.Errorf("i18n: read %s: %w", name, err)
		}
		catalog, err := parseCatalog(raw)
		if err != nil {
			return nil, fmt.Errorf("i18n: parse %s: %w", name, err)
		}
		bundle.catalogs[code] = catalog
	}

	if _, ok := bundle.catalogs[DefaultLanguage]; !ok {
		return nil, fmt.Errorf("i18n: default language %q catalog is required", DefaultLanguage)
	}

	bundle.languages = orderedLanguages(bundle.catalogs)
	return bundle, nil
}

// parseCatalog decodes one locale file. Each value is either a JSON string or a
// JSON object of plural forms.
func parseCatalog(raw []byte) (map[string]message, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}

	catalog := make(map[string]message, len(doc))
	for key, value := range doc {
		trimmed := strings.TrimSpace(string(value))
		if strings.HasPrefix(trimmed, "{") {
			var forms map[string]string
			if err := json.Unmarshal(value, &forms); err != nil {
				return nil, fmt.Errorf("key %q: %w", key, err)
			}
			catalog[key] = message{forms: forms}
			continue
		}
		var single string
		if err := json.Unmarshal(value, &single); err != nil {
			return nil, fmt.Errorf("key %q: %w", key, err)
		}
		catalog[key] = message{forms: map[string]string{"other": single}}
	}
	return catalog, nil
}

// orderedLanguages returns the supported languages with the default language
// first and the rest sorted by code, so the switcher renders deterministically.
func orderedLanguages(catalogs map[string]map[string]message) []Language {
	codes := make([]string, 0, len(catalogs))
	for code := range catalogs {
		codes = append(codes, code)
	}
	sort.Slice(codes, func(i, j int) bool {
		if codes[i] == DefaultLanguage {
			return true
		}
		if codes[j] == DefaultLanguage {
			return false
		}
		return codes[i] < codes[j]
	})

	languages := make([]Language, 0, len(codes))
	for _, code := range codes {
		languages = append(languages, languageFor(code))
	}
	return languages
}

// languageFor returns the metadata for a code, synthesising a reasonable entry
// (LTR, code as name) for codes without an explicit registration.
func languageFor(code string) Language {
	if meta, ok := languageMeta[code]; ok {
		return meta
	}
	dir := LTR
	if rtlLanguages[code] {
		dir = RTL
	}
	return Language{Code: code, Name: code, NativeName: code, Dir: dir}
}

// Languages returns the supported languages (default first) for the switcher.
func (b *Bundle) Languages() []Language {
	return b.languages
}

// HasLanguage reports whether the bundle has a catalog for code.
func (b *Bundle) HasLanguage(code string) bool {
	_, ok := b.catalogs[code]
	return ok
}

// Localizer binds a Bundle to a single active language. It is cheap to create
// per request.
type Localizer struct {
	bundle   *Bundle
	language Language
}

// Localizer returns a Localizer for code, falling back to the default language
// when the code is unknown.
func (b *Bundle) Localizer(code string) *Localizer {
	if !b.HasLanguage(code) {
		code = DefaultLanguage
	}
	return &Localizer{bundle: b, language: languageFor(code)}
}

// Lang returns the active language code (e.g. "en", "fa").
func (l *Localizer) Lang() string { return l.language.Code }

// Dir returns the active writing direction ("ltr" or "rtl").
func (l *Localizer) Dir() string { return string(l.language.Dir) }

// IsRTL reports whether the active language renders right-to-left.
func (l *Localizer) IsRTL() bool { return l.language.Dir == RTL }

// Languages exposes the bundle's supported languages so templates can render a
// switcher from a Localizer alone.
func (l *Localizer) Languages() []Language { return l.bundle.Languages() }

// T resolves key in the active language. Remaining arguments are alternating
// placeholder name/value pairs substituted into {name} markers, e.g.
//
//	loc.T("greeting", "name", "Ada") // "Hello, Ada"
//
// Resolution order: active language → default language → the key itself, so a
// missing translation degrades visibly but never blank.
func (l *Localizer) T(key string, args ...any) string {
	msg, ok := l.lookup(key)
	if !ok {
		return key
	}
	text, ok := msg.form("other")
	if !ok {
		return key
	}
	return substitute(text, args)
}

// Tn resolves a pluralized key, choosing the plural form for count in the
// active language. The count is always available to the template as {count};
// extra name/value pairs are substituted like T.
func (l *Localizer) Tn(key string, count int, args ...any) string {
	msg, ok := l.lookup(key)
	if !ok {
		return key
	}
	category := pluralCategory(l.language.Code, count)
	text, ok := msg.form(category)
	if !ok {
		return key
	}
	pairs := append([]any{"count", count}, args...)
	return substitute(text, pairs)
}

// Tsafe is like T but intended for catalog strings that contain trusted inline
// HTML markup (e.g. <code>, <strong>). The looked-up template string is trusted
// (it ships in our embedded catalogs, never from user input); only the
// substituted placeholder VALUES are HTML-escaped, so interpolating user data
// stays safe. The render layer wraps the result as template.HTML. Use plain T
// for everything that has no markup.
func (l *Localizer) Tsafe(key string, args ...any) string {
	msg, ok := l.lookup(key)
	if !ok {
		return key
	}
	text, ok := msg.form("other")
	if !ok {
		return key
	}
	return substituteEscaped(text, args)
}

// lookup finds a key in the active language, then the default language.
func (l *Localizer) lookup(key string) (message, bool) {
	if catalog, ok := l.bundle.catalogs[l.language.Code]; ok {
		if msg, ok := catalog[key]; ok {
			return msg, true
		}
	}
	if l.language.Code != DefaultLanguage {
		if catalog, ok := l.bundle.catalogs[DefaultLanguage]; ok {
			if msg, ok := catalog[key]; ok {
				return msg, true
			}
		}
	}
	return message{}, false
}

// substitute replaces {name} markers using alternating key/value pairs. Unknown
// markers are left intact; an odd trailing argument is ignored.
func substitute(text string, pairs []any) string {
	if len(pairs) < 2 {
		return text
	}
	replacements := make([]string, 0, len(pairs))
	for i := 0; i+1 < len(pairs); i += 2 {
		name, ok := pairs[i].(string)
		if !ok {
			continue
		}
		replacements = append(replacements, "{"+name+"}", fmt.Sprint(pairs[i+1]))
	}
	if len(replacements) == 0 {
		return text
	}
	return strings.NewReplacer(replacements...).Replace(text)
}

// substituteEscaped is substitute but HTML-escapes each value so trusted
// catalog markup can be combined with untrusted interpolated values safely.
func substituteEscaped(text string, pairs []any) string {
	if len(pairs) < 2 {
		return text
	}
	replacements := make([]string, 0, len(pairs))
	for i := 0; i+1 < len(pairs); i += 2 {
		name, ok := pairs[i].(string)
		if !ok {
			continue
		}
		replacements = append(replacements, "{"+name+"}", html.EscapeString(fmt.Sprint(pairs[i+1])))
	}
	if len(replacements) == 0 {
		return text
	}
	return strings.NewReplacer(replacements...).Replace(text)
}

// pluralCategory returns the CLDR plural category for count in the given
// language. Only the categories the supported languages actually use are
// implemented; unknown languages use the English rule. New locales with richer
// plural systems (Arabic, Russian, …) extend this switch.
func pluralCategory(lang string, count int) string {
	switch lang {
	case "fa":
		// Persian (CLDR): "one" covers 0 and 1, "other" everything else.
		if count == 0 || count == 1 {
			return "one"
		}
		return "other"
	default:
		// English-style: "one" only for exactly 1.
		if count == 1 {
			return "one"
		}
		return "other"
	}
}
