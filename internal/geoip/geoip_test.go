package geoip

import "testing"

func TestFlagEmoji(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string
	}{
		{"upper case US", "US", "\U0001F1FA\U0001F1F8"},
		{"lower case gb", "gb", "\U0001F1EC\U0001F1E7"},
		{"mixed case with spaces", "  De ", "\U0001F1E9\U0001F1EA"},
		{"empty", "", ""},
		{"single letter", "U", ""},
		{"three letters", "USA", ""},
		{"digits", "12", ""},
		{"unknown two letters", "ZZ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FlagEmoji(tc.code); got != tc.want {
				t.Fatalf("FlagEmoji(%q) = %q, want %q", tc.code, got, tc.want)
			}
		})
	}
}

func TestParseResponse(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantCode string
		wantOK   bool
	}{
		{"valid code", "US", "US", true},
		{"valid code with newline", "GB\n", "GB", true},
		{"lower case", "de\n", "DE", true},
		{"surrounding whitespace", "  fr  \n", "FR", true},
		{"empty", "", "", false},
		{"whitespace only", "  \n\t", "", false},
		{"garbage word", "error", "", false},
		{"json body", "{\"country\":\"US\"}", "", false},
		{"private ipv4", "192.168.1.1", "", false},
		{"loopback ipv4", "127.0.0.1", "", false},
		{"reserved ipv4", "10.0.0.5", "", false},
		{"unknown code", "ZZ", "", false},
		{"three letters", "USA", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotCode, gotOK := ParseResponse(tc.raw)
			if gotCode != tc.wantCode || gotOK != tc.wantOK {
				t.Fatalf("ParseResponse(%q) = (%q, %v), want (%q, %v)", tc.raw, gotCode, gotOK, tc.wantCode, tc.wantOK)
			}
		})
	}
}

func TestParseResponseThenFlag(t *testing.T) {
	// A successful parse must always yield a non-empty flag, and a failed parse
	// must always yield an empty flag (no phantom badge).
	if code, ok := ParseResponse("US"); !ok || FlagEmoji(code) == "" {
		t.Fatalf("valid response should produce a flag, got code=%q ok=%v flag=%q", code, ok, FlagEmoji(code))
	}
	if code, ok := ParseResponse("192.168.0.1"); ok || FlagEmoji(code) != "" {
		t.Fatalf("private IP should produce no flag, got code=%q ok=%v flag=%q", code, ok, FlagEmoji(code))
	}
}

func TestCountryName(t *testing.T) {
	if got := CountryName("US"); got != "United States" {
		t.Fatalf("CountryName(US) = %q, want United States", got)
	}
	if got := CountryName("gb"); got != "United Kingdom" {
		t.Fatalf("CountryName(gb) = %q, want United Kingdom", got)
	}
	if got := CountryName("ZZ"); got != "" {
		t.Fatalf("CountryName(ZZ) = %q, want empty", got)
	}
}

func TestCountryDataWellFormed(t *testing.T) {
	// Every recognised code must be exactly two upper-case ASCII letters and must
	// round-trip through FlagEmoji to a non-empty badge.
	if len(countryNames) < 200 {
		t.Fatalf("country table looks too small: %d entries", len(countryNames))
	}
	for code, name := range countryNames {
		if len(code) != 2 || !isASCIILetter(code[0]) || !isASCIILetter(code[1]) {
			t.Fatalf("malformed country code %q", code)
		}
		if code != toUpper(code) {
			t.Fatalf("country code %q is not upper-case", code)
		}
		if name == "" {
			t.Fatalf("country code %q has an empty name", code)
		}
		if FlagEmoji(code) == "" {
			t.Fatalf("country code %q produced no flag", code)
		}
	}
}

func toUpper(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 'a' - 'A'
		}
	}
	return string(b)
}
