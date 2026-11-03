package shell

import "codex_go/shellparse"

// ParsePlainPowerShellCommands mirrors Rust
// codex_shell_command::powershell::parse_powershell_script_into_plain_commands.
// The literal lowering itself lives in shellparse so the exec-policy
// dangerous-command classifier can apply the same strict subset without
// importing this package (shell already imports execpolicy).
func ParsePlainPowerShellCommands(script string) ([][]string, bool) {
	return shellparse.ParsePlainPowerShellCommands(script)
}
