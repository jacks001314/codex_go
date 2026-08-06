package onboarding

import (
	"context"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/auth"
)

func TestAuthFlowRendersRustLoginChoices(t *testing.T) {
	t.Setenv(auth.OpenAIAPIKeyEnv, "")
	model := newAuthFlowModel(context.Background(), AuthFlowOptions{
		CodexHome:      t.TempDir(),
		ChatGPTAllowed: true,
		APIKeyAllowed:  true,
	})
	view := model.View()
	for _, want := range []string{
		"Sign in with ChatGPT to use Codex as part of your paid plan",
		"or connect an API key for usage-based billing",
		"> 1. Sign in with ChatGPT",
		"Usage included with Plus, Pro, Business, and Enterprise plans",
		"2. Sign in with Device Code",
		"Sign in from another device with a one-time code",
		"3. Provide your own API key",
		"Pay for what you use",
		"Press enter to continue",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("auth view missing %q:\n%s", want, view)
		}
	}
}

func TestAuthFlowBrowserLoginCompletesAfterConfirmation(t *testing.T) {
	done := make(chan error, 1)
	services := defaultAuthFlowServices()
	services.startBrowser = func(ctx context.Context, options *auth.OAuthOptions) (*auth.BrowserLoginServer, error) {
		if !options.OpenBrowser {
			t.Fatal("browser login must request browser opening")
		}
		return &auth.BrowserLoginServer{
			AuthURL: "https://auth.example.test/login",
			Done:    done,
		}, nil
	}
	model := newAuthFlowModel(context.Background(), AuthFlowOptions{
		CodexHome:      t.TempDir(),
		ChatGPTAllowed: true,
		APIKeyAllowed:  true,
		services:       services,
	})

	_, start := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if start == nil || model.state != SignInChatGPTContinueInBrowser {
		t.Fatalf("browser start state=%s command=%v", model.state, start)
	}
	_, wait := model.Update(start())
	if wait == nil || !strings.Contains(model.View(), "https://auth.example.test/login") {
		t.Fatalf("browser waiting view:\n%s", model.View())
	}
	done <- nil
	model.Update(wait())
	if model.state != SignInChatGPTSuccessMessage || model.completed {
		t.Fatalf("browser completion state=%s completed=%v", model.state, model.completed)
	}
	_, quit := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if quit == nil || !model.completed || model.state != SignInChatGPTSuccess {
		t.Fatalf("browser confirmation state=%s completed=%v command=%v", model.state, model.completed, quit)
	}
}

func TestAuthFlowDeviceCodeCompletes(t *testing.T) {
	services := defaultAuthFlowServices()
	services.requestDeviceCode = func(ctx context.Context, options *auth.OAuthOptions) (*auth.DeviceCode, error) {
		return &auth.DeviceCode{
			VerificationURL: "https://auth.example.test/device",
			UserCode:        "CODE-X123",
			DeviceAuthID:    "device-auth-1",
		}, nil
	}
	completed := false
	services.completeDeviceCode = func(ctx context.Context, options *auth.OAuthOptions, code *auth.DeviceCode) error {
		completed = code != nil && code.UserCode == "CODE-X123"
		return nil
	}
	model := newAuthFlowModel(context.Background(), AuthFlowOptions{
		CodexHome:      t.TempDir(),
		ChatGPTAllowed: true,
		APIKeyAllowed:  true,
		services:       services,
	})

	_, start := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'2'}})
	if start == nil || model.state != SignInChatGPTDeviceCode {
		t.Fatalf("device start state=%s command=%v", model.state, start)
	}
	_, finish := model.Update(start())
	if finish == nil {
		t.Fatal("device completion command is nil")
	}
	view := model.View()
	if !strings.Contains(view, "https://auth.example.test/device") || !strings.Contains(view, "CODE-X123") {
		t.Fatalf("device view:\n%s", view)
	}
	model.Update(finish())
	if !completed || model.state != SignInChatGPTSuccessMessage {
		t.Fatalf("device completed=%v state=%s", completed, model.state)
	}
}

func TestAuthFlowAPIKeyPersistsAndCompletes(t *testing.T) {
	t.Setenv(auth.OpenAIAPIKeyEnv, "")
	home := t.TempDir()
	model := newAuthFlowModel(context.Background(), AuthFlowOptions{
		CodexHome:      home,
		ChatGPTAllowed: true,
		APIKeyAllowed:  true,
	})

	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'3'}})
	if model.state != SignInAPIKeyEntry {
		t.Fatalf("API key state=%s", model.state)
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune("sk-onboarding-test"), Paste: true})
	_, save := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if save == nil || !model.savingAPIKey {
		t.Fatalf("API key save command=%v saving=%v", save, model.savingAPIKey)
	}
	model.Update(save())
	if !model.completed || model.state != SignInAPIKeyConfigured {
		t.Fatalf("API key completion state=%s completed=%v error=%q", model.state, model.completed, model.errorMessage)
	}
	stored, err := auth.NewStore(home).Load()
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil || stored.OpenAIAPIKey != "sk-onboarding-test" {
		t.Fatalf("stored auth=%#v", stored)
	}
}

func TestAuthFlowPolicySkipsDisabledChoices(t *testing.T) {
	options := AuthOptionsForPolicy(false, true)
	if len(options) != 2 || options[0].Choice != AuthChoiceChatGPT || !options[0].Disabled || options[1].Choice != AuthChoiceAPIKey {
		t.Fatalf("policy options=%#v", options)
	}
	model := newAuthFlowModel(context.Background(), AuthFlowOptions{
		CodexHome:      t.TempDir(),
		ChatGPTAllowed: false,
		APIKeyAllowed:  true,
	})
	if model.highlighted != AuthChoiceAPIKey {
		t.Fatalf("highlighted=%s, want API key", model.highlighted)
	}
}
