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

// Rust #57626: the Bedrock wizard is offered whenever the effective provider is
// `openai`, whether chosen explicitly or left implicit, while a custom provider
// stays excluded even when it requires OpenAI auth.
func TestBedrockSetupWizardFollowsEffectiveProviderLikeRust(t *testing.T) {
	cases := []struct {
		name               string
		config             string
		wantShow           bool
		wantBedrockAllowed bool
	}{
		{
			name: "implicit openai",
			config: `[features]
bedrock_setup_wizard = true
`,
			wantShow:           true,
			wantBedrockAllowed: true,
		},
		{
			name: "explicit openai",
			config: `model_provider = "openai"

[features]
bedrock_setup_wizard = true
`,
			wantShow:           true,
			wantBedrockAllowed: true,
		},
		{
			name: "feature disabled",
			config: `model_provider = "openai"

[features]
bedrock_setup_wizard = false
`,
			wantShow:           true,
			wantBedrockAllowed: false,
		},
		{
			name: "custom provider requiring openai auth",
			config: `model_provider = "custom"

[model_providers.custom]
name = "Custom"
base_url = "http://127.0.0.1:1234/v1"
wire_api = "responses"
requires_openai_auth = true

[features]
bedrock_setup_wizard = true
`,
			wantShow:           true,
			wantBedrockAllowed: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearInteractiveAuthEnvironment(t)
			home := t.TempDir()
			t.Setenv("CODEX_HOME", home)
			if err := os.WriteFile(config.ConfigPath(home), []byte(tc.config), 0o600); err != nil {
				t.Fatal(err)
			}
			options, show, err := interactiveAuthOnboardingOptions(&cli.RootOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if show != tc.wantShow {
				t.Fatalf("show = %v, want %v", show, tc.wantShow)
			}
			if !tc.wantShow {
				return
			}
			if options == nil {
				t.Fatal("options = nil, want onboarding options")
			}
			if options.BedrockAllowed != tc.wantBedrockAllowed {
				t.Fatalf("BedrockAllowed = %v, want %v", options.BedrockAllowed, tc.wantBedrockAllowed)
			}
		})
	}
}
