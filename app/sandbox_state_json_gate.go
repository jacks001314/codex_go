package app

import "codex_go/cli"

// validateSandboxStateJSONBeforeConfig decodes `codex sandbox
// --sandbox-state-json` the way Rust's run_command_under_sandbox does: the
// state is deserialised, and its PathUri resolved for this host, before the
// `-c` overrides are parsed and long before the command is inspected. Go used
// to reach the override and command checks first, so a malformed state reported
// the wrong error.
func validateSandboxStateJSONBeforeConfig(parsed *cli.Parsed) error {
	if parsed == nil || parsed.Command != cli.CommandSandbox {
		return nil
	}
	if parsed.Sandbox.Setup || parsed.Sandbox.Uninstall {
		return nil
	}
	if !parsed.Sandbox.SandboxStateJSONProvided && parsed.Sandbox.SandboxStateJSON == "" {
		return nil
	}
	value, err := cli.ParseSandboxStateJSONValue(parsed.Sandbox.SandboxStateJSON)
	if err != nil {
		return err
	}
	native, err := value.NativeCWD()
	if err != nil {
		return err
	}
	// The sandbox runner expects a native absolute path, so hand it the same
	// payload with sandboxCwd rewritten rather than teaching it PathUri.
	parsed.Sandbox.SandboxStateJSON = value.NormalizedJSON(native)
	return nil
}
