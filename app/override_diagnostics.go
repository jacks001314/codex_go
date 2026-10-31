package app

import (
	"fmt"

	"codex_go/cli"
	"codex_go/config"
)

// overrideDiagnostic is one command's `-c`/`--config` handling the way Rust's
// CLI reports it: the override values in parse order (root scope first), the
// context Rust writes in front of a malformed value, and whether the text is a
// pre-rendered message that skips the runtime `Error: ` head.
type overrideDiagnostic struct {
	raw    []string
	prefix string
	silent bool
}

// validateCommandOverrides reproduces Rust's per-command override diagnostics.
// clap parses argv for every command, so this runs after cli.Parse and before
// the command body loads configuration.
func validateCommandOverrides(parsed *cli.Parsed) error {
	if parsed == nil {
		return nil
	}
	diag := commandOverrideDiagnostic(parsed)
	if len(diag.raw) == 0 {
		return nil
	}
	err := config.ValidateOverrides(diag.raw)
	if err == nil {
		return nil
	}
	message := diag.prefix + err.Error()
	if diag.silent {
		return exitMessagef("%s", message)
	}
	return fmt.Errorf("%s", message)
}

// commandOverrideDiagnostic maps a parsed command to the override scopes and
// prefix Rust uses. Commands Rust answers from clap, from sign-in checks or
// from install detection before configuration is parsed are left out on
// purpose.
func commandOverrideDiagnostic(parsed *cli.Parsed) overrideDiagnostic {
	switch parsed.Command {
	case cli.CommandExec, cli.CommandReview:
		// The exec family renders its own context, so the message appears
		// without the runtime `Error: ` head.
		return overrideDiagnostic{
			raw:    joinOverrideScopes(parsed.Root.ConfigOverrides, parsed.Exec.ConfigOverrides),
			prefix: "Error parsing -c overrides: ",
			silent: true,
		}
	case cli.CommandLogin, cli.CommandLogout:
		return overrideDiagnostic{
			raw:    joinOverrideScopes(parsed.Root.ConfigOverrides, parsed.Login.ConfigOverrides),
			prefix: "Error parsing -c overrides: ",
			silent: true,
		}
	case cli.CommandFeatures:
		return overrideDiagnostic{
			raw: joinOverrideScopes(parsed.Root.ConfigOverrides, parsed.Features.ConfigOverrides),
		}
	case cli.CommandPlugin:
		if parsed.Plugin.Action == "marketplace" {
			// The nested parser already prepended the enclosing scopes.
			return overrideDiagnostic{raw: parsed.Plugin.Marketplace.ConfigOverrides}
		}
		return overrideDiagnostic{
			raw: joinOverrideScopes(parsed.Root.ConfigOverrides, parsed.Plugin.ConfigOverrides),
		}
	case cli.CommandDebug:
		return overrideDiagnostic{
			raw: joinOverrideScopes(parsed.Root.ConfigOverrides, parsed.Debug.ConfigOverrides),
		}
	case cli.CommandMigrateRollouts:
		return overrideDiagnostic{
			raw: joinOverrideScopes(parsed.Root.ConfigOverrides, parsed.MigrateRollouts.ConfigOverrides),
		}
	case cli.CommandSandbox:
		if parsed.Sandbox.Uninstall {
			// `sandbox uninstall` runs before configuration is loaded, so Rust
			// reports the cleanup result instead of an override diagnostic.
			return overrideDiagnostic{}
		}
		return overrideDiagnostic{
			raw: joinOverrideScopes(parsed.Root.ConfigOverrides, parsed.Sandbox.ConfigOverrides),
		}
	}
	return overrideDiagnostic{}
}

// validateAppServerOverrides mirrors `codex app-server` startup, which parses
// the overrides with the server's own context prefix.
func validateAppServerOverrides(opts cli.AppServerOptions, root *cli.RootOptions) error {
	raw := joinOverrideScopes(rootOverrideScope(root), opts.ConfigOverrides)
	if err := config.ValidateOverrides(raw); err != nil {
		return fmt.Errorf("error parsing -c overrides: %v", err)
	}
	return nil
}

// validateAppServerDaemonOverrides mirrors `app-server daemon restart|update`,
// which parse the launch overrides as plain runtime errors. start, stop and
// version never load configuration, so they keep off the check.
func validateAppServerDaemonOverrides(opts cli.AppServerDaemonOptions, root *cli.RootOptions) error {
	switch opts.Action {
	case "restart", "update":
		return config.ValidateOverrides(joinOverrideScopes(rootOverrideScope(root), opts.ConfigOverrides))
	}
	return nil
}

func rootOverrideScope(root *cli.RootOptions) []string {
	if root == nil {
		return nil
	}
	return root.ConfigOverrides
}

// joinOverrideScopes concatenates override scopes in Rust's order: the root
// scope first, then the command's own scope.
func joinOverrideScopes(scopes ...[]string) []string {
	var out []string
	for _, scope := range scopes {
		out = append(out, scope...)
	}
	return out
}
