// Package ansi strips ANSI escape sequences from text captured for the SSR
// pages. SSH command output (notably the PasarGuard install/uninstall scripts)
// is colourised with ANSI SGR codes; the live command and install pages render
// plain text, so without stripping these the user sees raw "␛[0;91m" noise.
//
// The interactive xterm terminal does NOT use this — it forwards raw bytes to
// xterm.js, which interprets the colours. Stripping is only for the plain-text
// SSR consumers (commandstream + the install job output).
package ansi

import "regexp"

// ansiPattern matches the escape forms that appear in this codebase's output:
//   - CSI sequences (colours, cursor moves, erases): ESC '[' … final @–~
//   - charset-select escapes such as ESC '(' 'B' emitted by `tput sgr0`
//   - other simple two-byte Fe escapes
var ansiPattern = regexp.MustCompile(
	"\x1b\\[[0-9;?]*[ -/]*[@-~]" + // CSI
		"|\x1b[()][@-~]" + // charset select
		"|\x1b[@-Z\\\\^_]") // other Fe escapes

// Strip removes ANSI escape sequences from s. It is safe on text that contains
// none (returns s unchanged).
func Strip(s string) string {
	if s == "" {
		return s
	}
	return ansiPattern.ReplaceAllString(s, "")
}
