package cli

import "testing"

// TestGroupHelpRequestsLikeRust pins clap's routing for the group commands Rust
// declares with `arg_required_else_help`: a bare invocation asks for the short
// page on stderr with exit status 2, `-h` for the short page, and `--help` /
// `help` for the long page.
func TestGroupHelpRequestsLikeRust(t *testing.T) {
	groups := []struct {
		name string
		path []string
		kind GroupHelpKind
	}{
		{name: "mcp", path: []string{"mcp"}, kind: GroupHelpMCP},
		{name: "features", path: []string{"features"}, kind: GroupHelpFeatures},
		{name: "plugin", path: []string{"plugin"}, kind: GroupHelpPlugin},
		{name: "plugin marketplace", path: []string{"plugin", "marketplace"}, kind: GroupHelpPluginMarketplace},
		{name: "debug", path: []string{"debug"}, kind: GroupHelpDebug},
		{name: "debug app-server", path: []string{"debug", "app-server"}, kind: GroupHelpDebugAppServer},
		{name: "execpolicy", path: []string{"execpolicy"}, kind: GroupHelpExecpolicy},
		{name: "app-server daemon", path: []string{"app-server", "daemon"}, kind: GroupHelpAppServerDaemon},
	}
	for _, group := range groups {
		t.Run(group.name, func(t *testing.T) {
			bare := append(append([]string(nil), group.path...))
			parsed, err := Parse(bare)
			if err != nil {
				t.Fatalf("Parse(%v) error = %v", bare, err)
			}
			if parsed.GroupHelp == nil {
				t.Fatalf("Parse(%v) GroupHelp = nil", bare)
			}
			if parsed.GroupHelp.Kind != group.kind || !parsed.GroupHelp.Short || !parsed.GroupHelp.Required {
				t.Fatalf("Parse(%v) GroupHelp = %#v, want required short %q", bare, parsed.GroupHelp, group.kind)
			}

			short := append(append([]string(nil), group.path...), "-h")
			parsed, err = Parse(short)
			if err != nil {
				t.Fatalf("Parse(%v) error = %v", short, err)
			}
			if parsed.GroupHelp == nil || parsed.GroupHelp.Kind != group.kind || !parsed.GroupHelp.Short || parsed.GroupHelp.Required {
				t.Fatalf("Parse(%v) GroupHelp = %#v, want optional short %q", short, parsed.GroupHelp, group.kind)
			}

			for _, flag := range []string{"--help", "help"} {
				args := append(append([]string(nil), group.path...), flag)
				parsed, err = Parse(args)
				if err != nil {
					t.Fatalf("Parse(%v) error = %v", args, err)
				}
				if parsed.GroupHelp == nil || parsed.GroupHelp.Kind != group.kind || parsed.GroupHelp.Short || parsed.GroupHelp.Required {
					t.Fatalf("Parse(%v) GroupHelp = %#v, want long %q", args, parsed.GroupHelp, group.kind)
				}
			}

			// clap's help action fires as soon as the flag is parsed.
			trailing := append(append([]string(nil), group.path...), "--help", "--bogus")
			parsed, err = Parse(trailing)
			if err != nil {
				t.Fatalf("Parse(%v) error = %v", trailing, err)
			}
			if parsed.GroupHelp == nil || parsed.GroupHelp.Kind != group.kind || parsed.GroupHelp.Short {
				t.Fatalf("Parse(%v) GroupHelp = %#v, want long %q", trailing, parsed.GroupHelp, group.kind)
			}
		})
	}
}

// TestGroupHelpPagesArePopulatedLikeRust checks every captured page starts with
// clap's about line and ends with a single newline, which is what makes the
// output byte-identical to Rust.
func TestGroupHelpPagesArePopulatedLikeRust(t *testing.T) {
	about := map[GroupHelpKind]string{
		GroupHelpMCP:               "Manage external MCP servers for Codex",
		GroupHelpFeatures:          "Inspect feature flags",
		GroupHelpPlugin:            "Manage Codex plugins",
		GroupHelpPluginMarketplace: "Add, list, upgrade, or remove configured plugin marketplaces",
		GroupHelpDebug:             "Debugging tools",
		GroupHelpDebugAppServer:    "Tooling: helps debug the app server",
		GroupHelpExecpolicy:        "Execpolicy tooling",
		GroupHelpAppServerDaemon:   "Manage the local app-server daemon",
	}
	for kind, first := range about {
		for _, short := range []bool{false, true} {
			page := GroupHelpText(kind, short)
			if page == "" || page[len(page)-1] != '\n' || page[len(page)-2] == '\n' {
				t.Fatalf("GroupHelpText(%q, %v) is not a newline-terminated page", kind, short)
			}
			if len(page) < len(first) || page[:len(first)] != first {
				t.Fatalf("GroupHelpText(%q, %v) does not start with %q", kind, short, first)
			}
			if short == false && len(GroupHelpText(kind, true)) >= len(page) {
				t.Fatalf("GroupHelpText(%q) short page is not shorter than the long page", kind)
			}
		}
	}
}

// TestGroupMissingSubcommandErrorLikeRust pins clap's diagnostic for a group
// invoked with options but without its required subcommand.
func TestGroupMissingSubcommandErrorLikeRust(t *testing.T) {
	cases := []struct {
		args    []string
		message string
		usage   string
	}{
		{
			args:    []string{"mcp", "--enable", "zzz"},
			message: "'codex mcp' requires a subcommand but one was not provided\n  [subcommands: list, get, add, remove, login, logout, help]",
			usage:   "Usage: codex mcp [OPTIONS] <COMMAND>",
		},
		{
			args:    []string{"plugin", "marketplace", "-c", "a=b"},
			message: "'codex plugin marketplace' requires a subcommand but one was not provided\n  [subcommands: add, list, upgrade, remove, help]",
			usage:   "Usage: codex plugin marketplace [OPTIONS] <COMMAND>",
		},
		{
			args:    []string{"debug", "app-server", "--enable", "zzz"},
			message: "'codex debug app-server' requires a subcommand but one was not provided\n  [subcommands: send-message-v2, help]",
			usage:   "Usage: codex debug app-server [OPTIONS] <COMMAND>",
		},
		{
			args:    []string{"app-server", "daemon", "--enable", "zzz"},
			message: "'codex app-server daemon' requires a subcommand but one was not provided\n  [subcommands: bootstrap, start, restart, update, enable-remote-control, disable-remote-control, stop, version, pid-update-loop, help]",
			usage:   "Usage: codex app-server daemon [OPTIONS] <COMMAND>",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.args[0], func(t *testing.T) {
			usageErr := requireClapUsageError(t, testCase.args)
			if usageErr.Message != testCase.message {
				t.Fatalf("Parse(%v) message = %q, want %q", testCase.args, usageErr.Message, testCase.message)
			}
			if usageErr.Usage != testCase.usage {
				t.Fatalf("Parse(%v) usage = %q, want %q", testCase.args, usageErr.Usage, testCase.usage)
			}
		})
	}
}

// TestGroupLevelConfigOverridesLikeRust pins clap's flattened `-c`/`--config`
// on a group command: the value written before the subcommand belongs to the
// same override list the subcommand parser collects.
func TestGroupLevelConfigOverridesLikeRust(t *testing.T) {
	parsed, err := Parse([]string{"mcp", "-c", "a=b", "list"})
	if err != nil {
		t.Fatalf("Parse(mcp -c a=b list) error = %v", err)
	}
	if parsed.MCP.Action != "list" {
		t.Fatalf("mcp action = %q, want list", parsed.MCP.Action)
	}
	if got := len(parsed.MCP.ConfigOverrides); got != 1 || parsed.MCP.ConfigOverrides[0] != "a=b" {
		t.Fatalf("mcp overrides = %v, want [a=b]", parsed.MCP.ConfigOverrides)
	}

	parsed, err = Parse([]string{"plugin", "marketplace", "--config=a=b", "list"})
	if err != nil {
		t.Fatalf("Parse(plugin marketplace --config=a=b list) error = %v", err)
	}
	if parsed.Plugin.Marketplace.Action != "list" {
		t.Fatalf("marketplace action = %q, want list", parsed.Plugin.Marketplace.Action)
	}
	if got := len(parsed.Plugin.Marketplace.ConfigOverrides); got != 1 || parsed.Plugin.Marketplace.ConfigOverrides[0] != "a=b" {
		t.Fatalf("marketplace overrides = %v, want [a=b]", parsed.Plugin.Marketplace.ConfigOverrides)
	}

	usageErr := requireClapUsageError(t, []string{"debug", "-c"})
	if usageErr.Message != "a value is required for '--config <key=value>' but none was supplied" || usageErr.Usage != "" {
		t.Fatalf("Parse(debug -c) = %q / %q", usageErr.Message, usageErr.Usage)
	}
}
