package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	modelpkg "codex_go/model"
	"codex_go/tui/onboarding"
)

func runInteractiveEntry(ctx context.Context, root *cli.RootOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if shouldRunInteractiveAuthOnboarding(root, stdin, stdout) {
		remoteEndpoint, err := resolveInteractiveRemoteEndpoint(root)
		if err == nil && remoteEndpoint == nil {
			shouldExit, onboardingErr := runInteractiveAuthOnboarding(ctx, root, stdin, stdout)
			if onboardingErr != nil {
				return onboardingErr
			}
			if shouldExit {
				return nil
			}
		}
	}
	return runInteractive(ctx, root, stdin, stdout, stderr)
}

func shouldRunInteractiveAuthOnboarding(root *cli.RootOptions, stdin io.Reader, stdout io.Writer) bool {
	if !shouldRunInteractiveTUI(stdin, stdout) || strings.TrimSpace(os.Getenv("TERM")) == "dumb" {
		return false
	}
	if root == nil {
		return true
	}
	return strings.TrimSpace(root.Prompt) == ""
}

func runInteractiveAuthOnboarding(ctx context.Context, root *cli.RootOptions, stdin io.Reader, stdout io.Writer) (bool, error) {
	options, shouldShow, err := interactiveAuthOnboardingOptions(root)
	if err != nil || !shouldShow {
		return false, err
	}
	result, err := onboarding.RunAuthFlow(ctx, *options, stdin, stdout)
	if err != nil {
		return false, err
	}
	return result.ShouldExit, nil
}

func interactiveAuthOnboardingOptions(root *cli.RootOptions) (*onboarding.AuthFlowOptions, bool, error) {
	if root != nil && (root.Shared.OSS || strings.TrimSpace(root.Shared.OSSProvider) != "") {
		return nil, false, nil
	}
	codexHome := auth.DefaultCodexHome()
	loaded, err := config.LoadEffectiveWithOptions(codexHome, interactiveKeymapLoadOptions(root))
	if err != nil {
		return nil, false, fmt.Errorf("failed to load configuration for login: %w", err)
	}
	providerID := interactiveStringFromConfig(loaded.Values, "model_provider")
	provider, err := modelpkg.ProviderForConfigID(loaded.Values, providerID, interactiveStringFromConfig(loaded.Values, "openai_base_url"))
	if err != nil {
		return nil, false, err
	}
	if !provider.RequiresOpenAIAuth {
		return nil, false, nil
	}
	storeOptions := auth.StoreOptionsFromConfig(loaded.CLIAuthCredentialsStoreMode(), loaded.SecretAuthStorageEnabled())
	resolved, err := auth.NewStoreWithOptions(codexHome, storeOptions).Resolve()
	if err != nil {
		return nil, false, fmt.Errorf("failed to check login status: %w", err)
	}
	if resolved != nil && resolved.Auth.Mode() != "unknown" {
		return nil, false, nil
	}
	noAltScreen := root != nil && root.Shared.NoAltScreen
	return &onboarding.AuthFlowOptions{
		CodexHome:        codexHome,
		StoreOptions:     storeOptions,
		ChatGPTAllowed:   loaded.IsLoginMethodAllowed(config.ForcedLoginMethodChatGPT),
		APIKeyAllowed:    loaded.IsLoginMethodAllowed(config.ForcedLoginMethodAPI),
		ForcedWorkspaces: loaded.EffectiveChatGPTWorkspaces(),
		NoAltScreen:      noAltScreen,
	}, true, nil
}
