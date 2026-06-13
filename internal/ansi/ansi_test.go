package ansi

import "testing"

func TestStrip(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"no escapes", "node uninstalled successfully", "node uninstalled successfully"},
		{"sgr colour", "\x1b[0;91mAborted\x1b[0m", "Aborted"},
		{"reset only", "done\x1b[m", "done"},
		{"multiline colourised", "\x1b[0;96m=====\x1b[0m\n\x1b[0;92m  ok\x1b[0m", "=====\n  ok"},
		{"cursor and erase", "\x1b[2J\x1b[Hclear", "clear"},
		{"charset select from tput sgr0", "x\x1b(By", "xy"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Strip(c.in); got != c.want {
				t.Errorf("Strip(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
