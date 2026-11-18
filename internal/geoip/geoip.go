// Package geoip detects a server's country from its own public-IP egress and
// turns the resulting ISO 3166-1 alpha-2 code into a flag emoji.
//
// Detection deliberately runs ON the node over the already-established SSH
// connection (see Command) rather than from the Nodexia panel: a lookup made
// from the panel would report the panel's IP, not the node's, and the panel's
// own outbound network may be restricted. Running the probe remotely yields the
// node's real public IP and therefore its real country.
//
// Everything in this package is pure and network-free except for the remote
// Command string itself — the SSH round-trip is performed by the caller. That
// keeps parsing/flag logic fully unit-testable without touching the network.
package geoip

import "strings"

// Command is a POSIX-sh probe run over SSH on the target node. It asks a small
// set of keyless geo-IP services for the node's ISO 3166-1 alpha-2 country code
// and prints the first valid one (nothing else) to stdout.
//
// Robustness rules baked in:
//   - Prefer curl, fall back to wget, and exit cleanly (empty stdout) when
//     neither exists — the caller treats empty output as "no country".
//   - Each request has its own short timeout so a blocked/slow endpoint cannot
//     stall the whole probe; the endpoints are tried in order until one answers.
//   - Only a bare two-letter response is accepted (case "[A-Za-z][A-Za-z]"), so
//     error pages, JSON, rate-limit notices, or an echoed IP never leak through
//     as a bogus country.
//   - No single quotes appear inside the outer sh -c '...' wrapper, matching the
//     project's convention for generated remote shell.
//
// The endpoints all return a bare alpha-2 code (no JSON), which keeps both the
// shell and ParseResponse simple:
//   - https://ipinfo.io/country
//   - https://ifconfig.co/country-iso
//   - https://ipapi.co/country
const Command = `sh -c 'for u in https://ipinfo.io/country https://ifconfig.co/country-iso https://ipapi.co/country; do
  if command -v curl >/dev/null 2>&1; then
    r="$(curl -fsS --max-time 6 "$u" 2>/dev/null)"
  elif command -v wget >/dev/null 2>&1; then
    r="$(wget -qO- -T 6 "$u" 2>/dev/null)"
  else
    exit 0
  fi
  r="$(printf "%s" "$r" | tr -d "[:space:]")"
  case "$r" in
    [A-Za-z][A-Za-z]) printf "%s" "$r"; exit 0 ;;
  esac
done'`

// ParseResponse extracts a recognised ISO 3166-1 alpha-2 country code from the
// raw stdout of Command. The second return is false (and the code empty) for any
// output that is not a known two-letter code: an empty/blank response (curl or
// wget missing, no network, blocked endpoint), garbage, JSON, or an IP address
// (e.g. an RFC1918 address like "192.168.1.1" — not two letters, so rejected).
//
// Requiring the code to be a known country (CountryName non-empty) means stray
// two-letter tokens such as "XX" or "ZZ" are also rejected, never producing a
// phantom flag.
func ParseResponse(raw string) (string, bool) {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if len(code) != 2 || !isASCIILetter(code[0]) || !isASCIILetter(code[1]) {
		return "", false
	}
	if CountryName(code) == "" {
		return "", false
	}
	return code, true
}

// FlagEmoji converts an ISO 3166-1 alpha-2 code into its flag emoji built from
// the two Regional Indicator Symbols (U+1F1E6..U+1F1FF). It returns "" for any
// input that is not a recognised two-letter country code, so callers can safely
// render the result directly (empty = no badge).
//
// Platform caveat: some platforms — notably most Windows builds — do not ship
// flag-emoji glyphs and instead render the two Regional Indicator letters (e.g.
// "US") as a boxed letter pair. That is an acceptable, automatic degradation:
// the underlying code points still encode the country, so the badge reads as the
// ISO code rather than a flag. No image assets are used.
func FlagEmoji(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != 2 || !isASCIILetter(code[0]) || !isASCIILetter(code[1]) {
		return ""
	}
	if CountryName(code) == "" {
		return ""
	}
	const regionalIndicatorBase = 0x1F1E6 // 🇦, the Regional Indicator for 'A'
	first := rune(regionalIndicatorBase + int(code[0]-'A'))
	second := rune(regionalIndicatorBase + int(code[1]-'A'))
	return string([]rune{first, second})
}

func isASCIILetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// CountryName returns the English country name for an ISO 3166-1 alpha-2 code,
// or "" when the code is not recognised. It is the single source of truth for
// "is this a real country code" used by ParseResponse and FlagEmoji.
func CountryName(code string) string {
	return countryNames[strings.ToUpper(strings.TrimSpace(code))]
}
