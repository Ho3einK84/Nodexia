package commands

import (
	"sort"
	"strings"
	"sync"
)

// interactivePrograms are programs that require a TTY/PTY and therefore cannot
// run in the non-interactive command runner — they would just hang until the
// command timeout.  When one is detected the command center routes the user to
// the in-browser terminal (which runs them in a real PTY) instead.
var interactivePrograms = map[string]bool{
	// System monitors / TUIs
	"top": true, "htop": true, "btop": true, "atop": true, "glances": true,
	"nmon": true, "iftop": true, "iotop": true, "iptraf": true, "iptraf-ng": true,
	"ncdu": true, "k9s": true, "ctop": true, "lazydocker": true,
	// Editors
	"vi": true, "vim": true, "nvim": true, "nano": true, "emacs": true,
	"pico": true, "joe": true, "ed": true, "micro": true,
	// Pagers / manuals
	"less": true, "more": true, "most": true, "man": true,
	// Watchers
	"watch": true,
	// Database / language REPLs
	"mysql": true, "psql": true, "sqlite3": true, "redis-cli": true,
	"mongo": true, "mongosh": true, "python": true, "python3": true,
	"ipython": true, "node": true, "irb": true, "php": true, "lua": true,
	// Remote sessions
	"ssh": true, "telnet": true, "ftp": true, "sftp": true, "mosh": true,
	// Multiplexers
	"screen": true, "tmux": true, "byobu": true,
	// File managers
	"mc": true, "ranger": true, "vifm": true, "nnn": true, "lf": true,
	// Browsers
	"lynx": true, "w3m": true, "links": true, "links2": true, "elinks": true,
	// Config / interactive tools
	"alsamixer": true, "raspi-config": true, "nmtui": true, "visudo": true,
	"passwd": true, "su": true, "login": true, "dpkg-reconfigure": true,
	// Git / dev TUIs and debuggers
	"tig": true, "lazygit": true, "gdb": true, "lldb": true,
}

// commandWrappers are leading tokens that wrap the real command and should be
// skipped when locating the program name (e.g. `sudo -n htop`, `env FOO=bar vim`).
var commandWrappers = map[string]bool{
	"sudo": true, "env": true, "nice": true, "ionice": true, "nohup": true,
	"time": true, "exec": true, "command": true, "stdbuf": true,
	"setsid": true, "doas": true, "xargs": true,
}

// interactiveProgramsAttr returns the sorted, space-separated program list for
// the page's data attribute, so client-side hinting shares this single list.
var interactiveProgramsAttr = sync.OnceValue(func() string {
	names := make([]string, 0, len(interactivePrograms))
	for name := range interactivePrograms {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, " ")
})

// isInteractiveCommand reports whether command would require an interactive
// terminal.  It inspects each segment of a pipeline / compound command and the
// first real program in each, accounting for common wrappers and env prefixes.
func isInteractiveCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	for _, segment := range splitShellSegments(command) {
		if segmentIsInteractive(segment) {
			return true
		}
	}
	return false
}

// splitShellSegments breaks a command on the common shell separators so each
// stage of a pipeline or compound command is inspected independently.
func splitShellSegments(command string) []string {
	replacer := strings.NewReplacer(
		"&&", "\n",
		"||", "\n",
		"|", "\n",
		";", "\n",
		"&", "\n",
	)
	raw := strings.Split(replacer.Replace(command), "\n")
	segments := make([]string, 0, len(raw))
	for _, seg := range raw {
		if s := strings.TrimSpace(seg); s != "" {
			segments = append(segments, s)
		}
	}
	return segments
}

// segmentIsInteractive checks a single pipeline segment.
func segmentIsInteractive(segment string) bool {
	fields := strings.Fields(segment)
	i := 0
	for i < len(fields) {
		f := fields[i]
		if isEnvAssignment(f) {
			i++
			continue
		}
		if commandWrappers[basename(f)] {
			i++
			// Skip wrapper flags; consume an extra arg for option flags that take
			// a value (e.g. sudo -u root, ionice -c 2).
			for i < len(fields) && strings.HasPrefix(fields[i], "-") {
				flag := fields[i]
				i++
				if takesValue(flag) && i < len(fields) {
					i++
				}
			}
			continue
		}
		break
	}
	if i >= len(fields) {
		return false
	}

	prog := basename(fields[i])
	if interactivePrograms[prog] {
		return true
	}

	// Follow modes behave interactively (stream until interrupted).
	rest := fields[i+1:]
	switch prog {
	case "tail", "journalctl", "kubectl":
		return hasFollowFlag(rest)
	}
	return false
}

// isEnvAssignment reports whether a token is a NAME=value environment prefix.
func isEnvAssignment(token string) bool {
	eq := strings.IndexByte(token, '=')
	if eq <= 0 {
		return false
	}
	name := token[:eq]
	for idx, r := range name {
		isLetter := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		isDigit := r >= '0' && r <= '9'
		if idx == 0 && !isLetter {
			return false
		}
		if !isLetter && !isDigit {
			return false
		}
	}
	return true
}

// takesValue reports whether a wrapper flag consumes the following token.
// Limited to the user/group selectors common with sudo/doas (e.g. `sudo -u
// postgres psql`); boolean flags like sudo's -n are intentionally excluded so
// the program that follows them is still inspected.
func takesValue(flag string) bool {
	switch flag {
	case "-u", "-g", "-U":
		return true
	}
	return false
}

func hasFollowFlag(args []string) bool {
	for _, a := range args {
		if a == "-f" || a == "--follow" {
			return true
		}
		// Combined short flags such as -fn.
		if len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsRune(a, 'f') {
			return true
		}
	}
	return false
}

func basename(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}
