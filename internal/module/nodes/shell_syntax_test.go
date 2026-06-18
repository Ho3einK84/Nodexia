package nodes

import (
	"os/exec"
	"strings"
	"testing"
)

// TestGeneratedShellSyntax pipes every generated command through `sh -n` to
// catch quoting mistakes.
func TestGeneratedShellSyntax(t *testing.T) {
	commands := []string{
		PasarGuardProvider{}.DiscoveryCommand(),
		RebeccaProvider{}.DiscoveryCommand(),
	}
	for _, p := range DefaultProviders() {
		for _, a := range p.Actions() {
			cmd, _, err := p.ActionCommand("node2", a.Key)
			if err != nil {
				t.Fatalf("%s %s: %v", p.Type(), a.Key, err)
			}
			commands = append(commands, cmd)
		}
	}
	install, err := PasarGuardProvider{}.InstallCommand("node2", InstallConfig{ServicePort: "62011", Protocol: "grpc"})
	if err != nil {
		t.Fatal(err)
	}
	commands = append(commands, install)
	info, err := PasarGuardProvider{}.RegistrationInfoCommand("node2")
	if err != nil {
		t.Fatal(err)
	}
	commands = append(commands, info)

	configure, _, err := PasarGuardProvider{}.ConfigureCommand("node2", InstallConfig{
		ServicePort: "62055", APIPort: "62056", Protocol: "rest",
		APIKey: "11111111-2222-3333-4444-555555555555",
	})
	if err != nil {
		t.Fatal(err)
	}
	commands = append(commands, configure)

	rebeccaInstall, err := RebeccaProvider{}.InstallCommand(RebeccaInstallConfig{
		Channel:     "dev",
		ServicePort: "62050",
		APIPort:     "62051",
		Bundle:      testRebeccaBundle,
	})
	if err != nil {
		t.Fatal(err)
	}
	commands = append(commands, rebeccaInstall)

	for _, command := range commands {
		// Each command is "sh -c '<script>'" — extract the script and syntax-check it.
		inner := strings.TrimSuffix(strings.TrimPrefix(command, "sh -c '"), "'")
		if strings.Contains(inner, "'") {
			t.Errorf("command contains an unescaped single quote (would break sh -c wrapping):\n%s", command)
			continue
		}
		check := exec.Command("sh", "-n", "-c", inner)
		out, err := check.CombinedOutput()
		if err != nil {
			t.Errorf("sh -n failed: %v\n%s\ncommand:\n%s", err, out, inner)
		}
	}
}
