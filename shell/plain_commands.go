package shell

// ParsePlainShellCommands mirrors Rust parse_shell_script_into_commands: a
// script made only of literal words joined by `&&`, `||`, `;`, or `|` yields
// its individual commands. Anything the parser cannot see statically reports
// false so callers fall back to the raw argv instead of checking an incomplete
// view of the command.
func ParsePlainShellCommands(script string) ([][]string, bool) {
	return parseWordOnlyCommandsSequence(script)
}
