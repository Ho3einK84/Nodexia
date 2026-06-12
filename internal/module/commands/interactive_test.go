package commands

import "testing"

func TestIsInteractiveCommand(t *testing.T) {
	interactive := []string{
		"top",
		"htop",
		"vim /etc/hosts",
		"sudo vim /etc/hosts",
		"sudo -n htop",
		"env FOO=bar vim file",
		"DEBIAN_FRONTEND=noninteractive top",
		"/usr/bin/htop",
		"less /var/log/syslog",
		"man ssh",
		"mysql -u root -p",
		"psql",
		"ssh user@host",
		"watch -n1 date",
		"tail -f /var/log/syslog",
		"journalctl -f",
		"journalctl -u nginx --follow",
		"df -h | less",
		"cat foo && vim bar",
		"sudo -u postgres psql",
	}
	for _, cmd := range interactive {
		if !isInteractiveCommand(cmd) {
			t.Errorf("isInteractiveCommand(%q) = false, want true", cmd)
		}
	}

	nonInteractive := []string{
		"",
		"ls -la",
		"uname -a && uptime",
		"df -h",
		"free -h",
		"tail -n 100 /var/log/syslog",
		"journalctl -u nginx --no-pager",
		"systemctl status nginx",
		"echo top",          // 'top' only appears as an argument
		"grep vim file.txt", // 'vim' is an argument to grep
		"ps -eo pid,cmd",
		"apt-get -y upgrade",
		"cat /etc/os-release",
		"vimdiff-like-name", // not an exact program match
	}
	for _, cmd := range nonInteractive {
		if isInteractiveCommand(cmd) {
			t.Errorf("isInteractiveCommand(%q) = true, want false", cmd)
		}
	}
}

func TestTerminalRunURL(t *testing.T) {
	got := terminalRunURL(7, "tail -f /var/log/syslog")
	want := "/servers/7/terminal?init=tail+-f+%2Fvar%2Flog%2Fsyslog"
	if got != want {
		t.Errorf("terminalRunURL = %q, want %q", got, want)
	}
}
