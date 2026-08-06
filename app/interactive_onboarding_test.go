package app

import (
	"os"
	"testing"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
)

func TestInteractiveAuthOnboardingOptionsShowsOnlyWhenRequired(t *testing.T) {
	clearInteractiveAuthEnvironment(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	options, show, err := interactiveAuthOnboardingOptions(&cli.RootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !show || options == nil || !options.ChatGPTAllowed || !options.APIKeyAllowed {
		t.Fatalf("default onboarding options=%#v show=%v", options, show)
	}

	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-existing")); err != nil {
		t.Fatal(err)
	}
	options, show, err = interactiveAuthOnboardingOptions(&cli.RootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if show || options != nil {
		t.Fatalf("authenticated onboarding options=%#v show=%v", options, show)
	}
}

func TestInteractiveAuthOnboardingOptionsHonorsProviderAndLoginPolicy(t *testing.T) {
	t.Run("OSS flag", func(t *testing.T) {
		clearInteractiveAuthEnvironment(t)
		t.Setenv("CODEX_HOME", t.TempDir())
		options, show, err := interactiveAuthOnboardingOptions(&cli.RootOptions{Shared: cli.SharedOptions{OSS: true}})
		if err != nil {
			t.Fatal(err)
		}
		if show || options != nil {
			t.Fatalf("OSS onboarding options=%#v show=%v", options, show)
		}
	})

	t.Run("custom provider without OpenAI auth", func(t *testing.T) {
		clearInteractiveAuthEnvironment(t)
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		configText := `model_provider = "custom"

[model_providers.custom]
name = "Custom"
base_url = "http://127.0.0.1:1234/v1"
wire_api = "responses"
`
		if err := os.WriteFile(config.ConfigPath(home), []byte(configText), 0o600); err != nil {
			t.Fatal(err)
		}
		options, show, err := interactiveAuthOnboardingOptions(&cli.RootOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if show || options != nil {
			t.Fatalf("custom provider onboarding options=%#v show=%v", options, show)
		}
	})

	t.Run("forced ChatGPT login", func(t *testing.T) {
		clearInteractiveAuthEnvironment(t)
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		if err := os.WriteFile(config.ConfigPath(home), []byte(`forced_login_method = "chatgpt"`), 0o600); err != nil {
			t.Fatal(err)
		}
		options, show, err := interactiveAuthOnboardingOptions(&cli.RootOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !show || options == nil || !options.ChatGPTAllowed || options.APIKeyAllowed {
			t.Fatalf("forced login options=%#v show=%v", options, show)
		}
	})
}

func clearInteractiveAuthEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv(auth.OpenAIAPIKeyEnv, "")
	t.Setenv(auth.CodexAPIKeyEnv, "")
	t.Setenv(auth.CodexAccessTokenEnv, "")
}
