package app

import (
	"context"
	"fmt"
	"io"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
)

// interactiveStartupConfigServerLogin is the login entry point used during TUI
// startup; tests replace it to observe the decision without a browser flow.
var interactiveStartupConfigServerLogin = ensureConfigServerLogin

// ensureInteractiveStartupConfigServerLogin mirrors Rust #57175
// (tui/src/startup_orchestration.rs): an interactive launch whose effective
// configuration requires a customer-hosted configuration server signs in before
// the TUI takes over the terminal. That way the first thread bootstrap, the
// managed bundle, and onboarding all see the same authenticated configuration.
// Configuration that fails to load is left to the normal startup error path.
func ensureInteractiveStartupConfigServerLogin(ctx context.Context, root *cli.RootOptions, stderr io.Writer) error {
	codexHome := auth.DefaultCodexHome()
	if codexHome == "" {
		return nil
	}
	loaded, err := config.LoadEffectiveWithOptions(codexHome, interactiveKeymapLoadOptions(root))
	if err != nil || loaded == nil {
		return nil
	}
	if !loaded.RequiresRemoteConfig() {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return interactiveStartupConfigServerLogin(ctx, codexHome, loaded.Requirements, func(url string) {
		if stderr == nil {
			return
		}
		fmt.Fprintf(stderr, "Sign in to the configuration server. If your browser did not open, navigate to this URL:\n\n%s\n\n", url)
	})
}
