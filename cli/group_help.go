package cli

import (
	"fmt"
	"strings"
)

// GroupHelpKind identifies which clap group page to print. Rust declares these
// commands with `arg_required_else_help`, so a bare invocation prints the short
// page on stderr and exits 2, while `-h` prints the short page and `--help` /
// `help` print the long page on stdout.
type GroupHelpKind string

const (
	GroupHelpMCP               GroupHelpKind = "mcp"
	GroupHelpFeatures          GroupHelpKind = "features"
	GroupHelpPlugin            GroupHelpKind = "plugin"
	GroupHelpPluginMarketplace GroupHelpKind = "plugin marketplace"
	GroupHelpDebug             GroupHelpKind = "debug"
	GroupHelpDebugAppServer    GroupHelpKind = "debug app-server"
	GroupHelpExecpolicy        GroupHelpKind = "execpolicy"
	GroupHelpAppServerDaemon   GroupHelpKind = "app-server daemon"
)

// GroupHelpText renders the clap page Rust 0.166 prints for a group command.
// `short` selects clap's `-h` summary layout. The pages are captured verbatim
// from the 0.166 build so they are byte-identical to Rust.
func GroupHelpText(kind GroupHelpKind, short bool) string {
	pages := groupHelpLongPages
	if short {
		pages = groupHelpShortPages
	}
	lines, ok := pages[kind]
	if !ok {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

var groupHelpLongPages = map[GroupHelpKind][]string{
	GroupHelpMCP: {
		"Manage external MCP servers for Codex",
		"",
		"Usage: codex mcp [OPTIONS] <COMMAND>",
		"",
		"Commands:",
		"  list    ",
		"  get     ",
		"  add     ",
		"  remove  ",
		"  login   ",
		"  logout  ",
		"  help    Print this message or the help of the given subcommand(s)",
		"",
		"Options:",
		"  -c, --config <key=value>",
		"          Override a configuration value that would otherwise be loaded from `~/.codex/config.toml`.",
		"          Use a dotted path (`foo.bar.baz`) to override nested values. The `value` portion is parsed",
		"          as TOML. If it fails to parse as TOML, the raw string is used as a literal.",
		"          ",
		"          Examples: - `-c model=\"o3\"` - `-c 'sandbox_permissions=[\"disk-full-read-access\"]'` - `-c",
		"          shell_environment_policy.inherit=all`",
		"",
		"      --enable <FEATURE>",
		"          Enable a feature (repeatable). Equivalent to `-c features.<name>=true`",
		"",
		"      --disable <FEATURE>",
		"          Disable a feature (repeatable). Equivalent to `-c features.<name>=false`",
		"",
		"  -h, --help",
		"          Print help (see a summary with '-h')",
	},
	GroupHelpFeatures: {
		"Inspect feature flags",
		"",
		"Usage: codex features [OPTIONS] <COMMAND>",
		"",
		"Commands:",
		"  list     List known features with their stage and effective state",
		"  enable   Enable a feature in config.toml",
		"  disable  Disable a feature in config.toml",
		"  help     Print this message or the help of the given subcommand(s)",
		"",
		"Options:",
		"  -c, --config <key=value>",
		"          Override a configuration value that would otherwise be loaded from `~/.codex/config.toml`.",
		"          Use a dotted path (`foo.bar.baz`) to override nested values. The `value` portion is parsed",
		"          as TOML. If it fails to parse as TOML, the raw string is used as a literal.",
		"          ",
		"          Examples: - `-c model=\"o3\"` - `-c 'sandbox_permissions=[\"disk-full-read-access\"]'` - `-c",
		"          shell_environment_policy.inherit=all`",
		"",
		"      --enable <FEATURE>",
		"          Enable a feature (repeatable). Equivalent to `-c features.<name>=true`",
		"",
		"      --disable <FEATURE>",
		"          Disable a feature (repeatable). Equivalent to `-c features.<name>=false`",
		"",
		"  -h, --help",
		"          Print help (see a summary with '-h')",
	},
	GroupHelpPlugin: {
		"Manage Codex plugins",
		"",
		"Usage: codex plugin [OPTIONS] <COMMAND>",
		"",
		"Commands:",
		"  add          Install a plugin from a configured or remote marketplace",
		"  list         List plugins available from configured and remote marketplaces",
		"  marketplace  Add, list, upgrade, or remove configured plugin marketplaces",
		"  remove       Uninstall a plugin and remove its local cache",
		"  help         Print this message or the help of the given subcommand(s)",
		"",
		"Options:",
		"  -c, --config <key=value>",
		"          Override a configuration value that would otherwise be loaded from `~/.codex/config.toml`.",
		"          Use a dotted path (`foo.bar.baz`) to override nested values. The `value` portion is parsed",
		"          as TOML. If it fails to parse as TOML, the raw string is used as a literal.",
		"          ",
		"          Examples: - `-c model=\"o3\"` - `-c 'sandbox_permissions=[\"disk-full-read-access\"]'` - `-c",
		"          shell_environment_policy.inherit=all`",
		"",
		"      --enable <FEATURE>",
		"          Enable a feature (repeatable). Equivalent to `-c features.<name>=true`",
		"",
		"      --disable <FEATURE>",
		"          Disable a feature (repeatable). Equivalent to `-c features.<name>=false`",
		"",
		"  -h, --help",
		"          Print help (see a summary with '-h')",
	},
	GroupHelpPluginMarketplace: {
		"Add, list, upgrade, or remove configured plugin marketplaces",
		"",
		"Usage: codex plugin marketplace [OPTIONS] <COMMAND>",
		"",
		"Commands:",
		"  add      Add a local or Git marketplace to the configured marketplace sources",
		"  list     List plugin marketplaces Codex is currently considering and their roots",
		"  upgrade  Refresh configured Git marketplace snapshots",
		"  remove   Remove a configured marketplace source by name",
		"  help     Print this message or the help of the given subcommand(s)",
		"",
		"Options:",
		"  -c, --config <key=value>",
		"          Override a configuration value that would otherwise be loaded from `~/.codex/config.toml`.",
		"          Use a dotted path (`foo.bar.baz`) to override nested values. The `value` portion is parsed",
		"          as TOML. If it fails to parse as TOML, the raw string is used as a literal.",
		"          ",
		"          Examples: - `-c model=\"o3\"` - `-c 'sandbox_permissions=[\"disk-full-read-access\"]'` - `-c",
		"          shell_environment_policy.inherit=all`",
		"",
		"      --enable <FEATURE>",
		"          Enable a feature (repeatable). Equivalent to `-c features.<name>=true`",
		"",
		"      --disable <FEATURE>",
		"          Disable a feature (repeatable). Equivalent to `-c features.<name>=false`",
		"",
		"  -h, --help",
		"          Print help (see a summary with '-h')",
	},
	GroupHelpDebug: {
		"Debugging tools",
		"",
		"Usage: codex debug [OPTIONS] <COMMAND>",
		"",
		"Commands:",
		"  models        Render the raw model catalog as JSON",
		"  app-server    Tooling: helps debug the app server",
		"  prompt-input  Render the model-visible prompt input list as JSON",
		"  help          Print this message or the help of the given subcommand(s)",
		"",
		"Options:",
		"  -c, --config <key=value>",
		"          Override a configuration value that would otherwise be loaded from `~/.codex/config.toml`.",
		"          Use a dotted path (`foo.bar.baz`) to override nested values. The `value` portion is parsed",
		"          as TOML. If it fails to parse as TOML, the raw string is used as a literal.",
		"          ",
		"          Examples: - `-c model=\"o3\"` - `-c 'sandbox_permissions=[\"disk-full-read-access\"]'` - `-c",
		"          shell_environment_policy.inherit=all`",
		"",
		"      --enable <FEATURE>",
		"          Enable a feature (repeatable). Equivalent to `-c features.<name>=true`",
		"",
		"      --disable <FEATURE>",
		"          Disable a feature (repeatable). Equivalent to `-c features.<name>=false`",
		"",
		"  -h, --help",
		"          Print help (see a summary with '-h')",
	},
	GroupHelpDebugAppServer: {
		"Tooling: helps debug the app server",
		"",
		"Usage: codex debug app-server [OPTIONS] <COMMAND>",
		"",
		"Commands:",
		"  send-message-v2  ",
		"  help             Print this message or the help of the given subcommand(s)",
		"",
		"Options:",
		"  -c, --config <key=value>",
		"          Override a configuration value that would otherwise be loaded from `~/.codex/config.toml`.",
		"          Use a dotted path (`foo.bar.baz`) to override nested values. The `value` portion is parsed",
		"          as TOML. If it fails to parse as TOML, the raw string is used as a literal.",
		"          ",
		"          Examples: - `-c model=\"o3\"` - `-c 'sandbox_permissions=[\"disk-full-read-access\"]'` - `-c",
		"          shell_environment_policy.inherit=all`",
		"",
		"      --enable <FEATURE>",
		"          Enable a feature (repeatable). Equivalent to `-c features.<name>=true`",
		"",
		"      --disable <FEATURE>",
		"          Disable a feature (repeatable). Equivalent to `-c features.<name>=false`",
		"",
		"  -h, --help",
		"          Print help (see a summary with '-h')",
	},
	GroupHelpExecpolicy: {
		"Execpolicy tooling",
		"",
		"Usage: codex execpolicy [OPTIONS] <COMMAND>",
		"",
		"Commands:",
		"  check  Check execpolicy files against a command",
		"  help   Print this message or the help of the given subcommand(s)",
		"",
		"Options:",
		"  -c, --config <key=value>",
		"          Override a configuration value that would otherwise be loaded from `~/.codex/config.toml`.",
		"          Use a dotted path (`foo.bar.baz`) to override nested values. The `value` portion is parsed",
		"          as TOML. If it fails to parse as TOML, the raw string is used as a literal.",
		"          ",
		"          Examples: - `-c model=\"o3\"` - `-c 'sandbox_permissions=[\"disk-full-read-access\"]'` - `-c",
		"          shell_environment_policy.inherit=all`",
		"",
		"      --enable <FEATURE>",
		"          Enable a feature (repeatable). Equivalent to `-c features.<name>=true`",
		"",
		"      --disable <FEATURE>",
		"          Disable a feature (repeatable). Equivalent to `-c features.<name>=false`",
		"",
		"  -h, --help",
		"          Print help (see a summary with '-h')",
	},
	GroupHelpAppServerDaemon: {
		"Manage the local app-server daemon",
		"",
		"Usage: codex app-server daemon [OPTIONS] <COMMAND>",
		"",
		"Commands:",
		"  bootstrap               Install durable local app-server management for SSH-driven use",
		"  start                   Start the local app server daemon if it is not already running",
		"  restart                 Restart the local app server daemon",
		"  update                  Update the daemon package (may interrupt running work)",
		"  enable-remote-control   Enable remote control for future starts and a currently running managed",
		"                          daemon",
		"  disable-remote-control  Disable remote control for future starts and a currently running managed",
		"                          daemon",
		"  stop                    Stop the local app server daemon",
		"  version                 Print local CLI and running app-server versions as JSON",
		"  help                    Print this message or the help of the given subcommand(s)",
		"",
		"Options:",
		"  -c, --config <key=value>",
		"          Override a configuration value that would otherwise be loaded from `~/.codex/config.toml`.",
		"          Use a dotted path (`foo.bar.baz`) to override nested values. The `value` portion is parsed",
		"          as TOML. If it fails to parse as TOML, the raw string is used as a literal.",
		"          ",
		"          Examples: - `-c model=\"o3\"` - `-c 'sandbox_permissions=[\"disk-full-read-access\"]'` - `-c",
		"          shell_environment_policy.inherit=all`",
		"",
		"      --enable <FEATURE>",
		"          Enable a feature (repeatable). Equivalent to `-c features.<name>=true`",
		"",
		"      --disable <FEATURE>",
		"          Disable a feature (repeatable). Equivalent to `-c features.<name>=false`",
		"",
		"  -h, --help",
		"          Print help (see a summary with '-h')",
	},
}

var groupHelpShortPages = map[GroupHelpKind][]string{
	GroupHelpMCP: {
		"Manage external MCP servers for Codex",
		"",
		"Usage: codex mcp [OPTIONS] <COMMAND>",
		"",
		"Commands:",
		"  list    ",
		"  get     ",
		"  add     ",
		"  remove  ",
		"  login   ",
		"  logout  ",
		"  help    Print this message or the help of the given subcommand(s)",
		"",
		"Options:",
		"  -c, --config <key=value>  Override a configuration value that would otherwise be loaded from",
		"                            `~/.codex/config.toml`. Use a dotted path (`foo.bar.baz`) to override",
		"                            nested values. The `value` portion is parsed as TOML. If it fails to",
		"                            parse as TOML, the raw string is used as a literal",
		"      --enable <FEATURE>    Enable a feature (repeatable). Equivalent to `-c features.<name>=true`",
		"      --disable <FEATURE>   Disable a feature (repeatable). Equivalent to `-c features.<name>=false`",
		"  -h, --help                Print help (see more with '--help')",
	},
	GroupHelpFeatures: {
		"Inspect feature flags",
		"",
		"Usage: codex features [OPTIONS] <COMMAND>",
		"",
		"Commands:",
		"  list     List known features with their stage and effective state",
		"  enable   Enable a feature in config.toml",
		"  disable  Disable a feature in config.toml",
		"  help     Print this message or the help of the given subcommand(s)",
		"",
		"Options:",
		"  -c, --config <key=value>  Override a configuration value that would otherwise be loaded from",
		"                            `~/.codex/config.toml`. Use a dotted path (`foo.bar.baz`) to override",
		"                            nested values. The `value` portion is parsed as TOML. If it fails to",
		"                            parse as TOML, the raw string is used as a literal",
		"      --enable <FEATURE>    Enable a feature (repeatable). Equivalent to `-c features.<name>=true`",
		"      --disable <FEATURE>   Disable a feature (repeatable). Equivalent to `-c features.<name>=false`",
		"  -h, --help                Print help (see more with '--help')",
	},
	GroupHelpPlugin: {
		"Manage Codex plugins",
		"",
		"Usage: codex plugin [OPTIONS] <COMMAND>",
		"",
		"Commands:",
		"  add          Install a plugin from a configured or remote marketplace",
		"  list         List plugins available from configured and remote marketplaces",
		"  marketplace  Add, list, upgrade, or remove configured plugin marketplaces",
		"  remove       Uninstall a plugin and remove its local cache",
		"  help         Print this message or the help of the given subcommand(s)",
		"",
		"Options:",
		"  -c, --config <key=value>  Override a configuration value that would otherwise be loaded from",
		"                            `~/.codex/config.toml`. Use a dotted path (`foo.bar.baz`) to override",
		"                            nested values. The `value` portion is parsed as TOML. If it fails to",
		"                            parse as TOML, the raw string is used as a literal",
		"      --enable <FEATURE>    Enable a feature (repeatable). Equivalent to `-c features.<name>=true`",
		"      --disable <FEATURE>   Disable a feature (repeatable). Equivalent to `-c features.<name>=false`",
		"  -h, --help                Print help (see more with '--help')",
	},
	GroupHelpPluginMarketplace: {
		"Add, list, upgrade, or remove configured plugin marketplaces",
		"",
		"Usage: codex plugin marketplace [OPTIONS] <COMMAND>",
		"",
		"Commands:",
		"  add      Add a local or Git marketplace to the configured marketplace sources",
		"  list     List plugin marketplaces Codex is currently considering and their roots",
		"  upgrade  Refresh configured Git marketplace snapshots",
		"  remove   Remove a configured marketplace source by name",
		"  help     Print this message or the help of the given subcommand(s)",
		"",
		"Options:",
		"  -c, --config <key=value>  Override a configuration value that would otherwise be loaded from",
		"                            `~/.codex/config.toml`. Use a dotted path (`foo.bar.baz`) to override",
		"                            nested values. The `value` portion is parsed as TOML. If it fails to",
		"                            parse as TOML, the raw string is used as a literal",
		"      --enable <FEATURE>    Enable a feature (repeatable). Equivalent to `-c features.<name>=true`",
		"      --disable <FEATURE>   Disable a feature (repeatable). Equivalent to `-c features.<name>=false`",
		"  -h, --help                Print help (see more with '--help')",
	},
	GroupHelpDebug: {
		"Debugging tools",
		"",
		"Usage: codex debug [OPTIONS] <COMMAND>",
		"",
		"Commands:",
		"  models        Render the raw model catalog as JSON",
		"  app-server    Tooling: helps debug the app server",
		"  prompt-input  Render the model-visible prompt input list as JSON",
		"  help          Print this message or the help of the given subcommand(s)",
		"",
		"Options:",
		"  -c, --config <key=value>  Override a configuration value that would otherwise be loaded from",
		"                            `~/.codex/config.toml`. Use a dotted path (`foo.bar.baz`) to override",
		"                            nested values. The `value` portion is parsed as TOML. If it fails to",
		"                            parse as TOML, the raw string is used as a literal",
		"      --enable <FEATURE>    Enable a feature (repeatable). Equivalent to `-c features.<name>=true`",
		"      --disable <FEATURE>   Disable a feature (repeatable). Equivalent to `-c features.<name>=false`",
		"  -h, --help                Print help (see more with '--help')",
	},
	GroupHelpDebugAppServer: {
		"Tooling: helps debug the app server",
		"",
		"Usage: codex debug app-server [OPTIONS] <COMMAND>",
		"",
		"Commands:",
		"  send-message-v2  ",
		"  help             Print this message or the help of the given subcommand(s)",
		"",
		"Options:",
		"  -c, --config <key=value>  Override a configuration value that would otherwise be loaded from",
		"                            `~/.codex/config.toml`. Use a dotted path (`foo.bar.baz`) to override",
		"                            nested values. The `value` portion is parsed as TOML. If it fails to",
		"                            parse as TOML, the raw string is used as a literal",
		"      --enable <FEATURE>    Enable a feature (repeatable). Equivalent to `-c features.<name>=true`",
		"      --disable <FEATURE>   Disable a feature (repeatable). Equivalent to `-c features.<name>=false`",
		"  -h, --help                Print help (see more with '--help')",
	},
	GroupHelpExecpolicy: {
		"Execpolicy tooling",
		"",
		"Usage: codex execpolicy [OPTIONS] <COMMAND>",
		"",
		"Commands:",
		"  check  Check execpolicy files against a command",
		"  help   Print this message or the help of the given subcommand(s)",
		"",
		"Options:",
		"  -c, --config <key=value>  Override a configuration value that would otherwise be loaded from",
		"                            `~/.codex/config.toml`. Use a dotted path (`foo.bar.baz`) to override",
		"                            nested values. The `value` portion is parsed as TOML. If it fails to",
		"                            parse as TOML, the raw string is used as a literal",
		"      --enable <FEATURE>    Enable a feature (repeatable). Equivalent to `-c features.<name>=true`",
		"      --disable <FEATURE>   Disable a feature (repeatable). Equivalent to `-c features.<name>=false`",
		"  -h, --help                Print help (see more with '--help')",
	},
	GroupHelpAppServerDaemon: {
		"Manage the local app-server daemon",
		"",
		"Usage: codex app-server daemon [OPTIONS] <COMMAND>",
		"",
		"Commands:",
		"  bootstrap               Install durable local app-server management for SSH-driven use",
		"  start                   Start the local app server daemon if it is not already running",
		"  restart                 Restart the local app server daemon",
		"  update                  Update the daemon package (may interrupt running work)",
		"  enable-remote-control   Enable remote control for future starts and a currently running managed",
		"                          daemon",
		"  disable-remote-control  Disable remote control for future starts and a currently running managed",
		"                          daemon",
		"  stop                    Stop the local app server daemon",
		"  version                 Print local CLI and running app-server versions as JSON",
		"  help                    Print this message or the help of the given subcommand(s)",
		"",
		"Options:",
		"  -c, --config <key=value>  Override a configuration value that would otherwise be loaded from",
		"                            `~/.codex/config.toml`. Use a dotted path (`foo.bar.baz`) to override",
		"                            nested values. The `value` portion is parsed as TOML. If it fails to",
		"                            parse as TOML, the raw string is used as a literal",
		"      --enable <FEATURE>    Enable a feature (repeatable). Equivalent to `-c features.<name>=true`",
		"      --disable <FEATURE>   Disable a feature (repeatable). Equivalent to `-c features.<name>=false`",
		"  -h, --help                Print help (see more with '--help')",
	},
}

// groupSubcommands lists the names clap prints in its
// `[subcommands: ...]` line for each group command.
var groupSubcommands = map[GroupHelpKind][]string{
	GroupHelpMCP:               {"list", "get", "add", "remove", "login", "logout", "help"},
	GroupHelpFeatures:          {"list", "enable", "disable", "help"},
	GroupHelpPlugin:            {"add", "list", "marketplace", "remove", "help"},
	GroupHelpPluginMarketplace: {"add", "list", "upgrade", "remove", "help"},
	GroupHelpDebug:             {"models", "app-server", "prompt-input", "trace-reduce", "clear-memories", "help"},
	GroupHelpDebugAppServer:    {"send-message-v2", "help"},
	GroupHelpExecpolicy:        {"check", "help"},
	GroupHelpAppServerDaemon:   {"bootstrap", "start", "restart", "update", "enable-remote-control", "disable-remote-control", "stop", "version", "pid-update-loop", "help"},
}

// MissingSubcommandError mirrors clap's diagnostic for a group command that was
// invoked with options but without its required subcommand. A bare invocation
// prints the short help page instead (see GroupHelpRequest.Required).
func MissingSubcommandError(kind GroupHelpKind) error {
	names, ok := groupSubcommands[kind]
	if !ok {
		return fmt.Errorf("%s requires a subcommand", kind)
	}
	return &UsageError{
		Message: "'codex " + string(kind) + "' requires a subcommand but one was not provided\n  [subcommands: " + strings.Join(names, ", ") + "]",
		Usage:   "Usage: codex " + string(kind) + " [OPTIONS] <COMMAND>",
	}
}

// GroupHelpKindForPath resolves the group page for a command path. The nested
// groups (`plugin marketplace`, `debug app-server`, `app-server daemon`) are
// separate clap commands with their own pages.
func GroupHelpKindForPath(command Command, args []string) (GroupHelpKind, int, bool) {
	switch command {
	case CommandMCP:
		return GroupHelpMCP, 0, true
	case CommandFeatures:
		return GroupHelpFeatures, 0, true
	case CommandPlugin:
		if len(args) > 0 && args[0] == "marketplace" {
			return GroupHelpPluginMarketplace, 1, true
		}
		return GroupHelpPlugin, 0, true
	case CommandDebug:
		if len(args) > 0 && args[0] == "app-server" {
			return GroupHelpDebugAppServer, 1, true
		}
		return GroupHelpDebug, 0, true
	case CommandExecpolicy:
		return GroupHelpExecpolicy, 0, true
	case CommandAppServer:
		if len(args) > 0 && args[0] == "daemon" {
			return GroupHelpAppServerDaemon, 1, true
		}
		return "", 0, false
	default:
		return "", 0, false
	}
}
