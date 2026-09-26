package onboarding

import (
	"context"
	"errors"
	"net/http"
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

// Rust #47411 binds the local application policy to the embedded login flow; the
// flow must hand that client to every ChatGPT login request it makes.
func TestAuthFlowCarriesTheBoundClientLikeRust(t *testing.T) {
	bound := &http.Client{}
	seen := make([]*http.Client, 0, 2)
	services := defaultAuthFlowServices()
	services.requestDeviceCode = func(_ context.Context, options *auth.OAuthOptions) (*auth.DeviceCode, error) {
		seen = append(seen, options.HTTPClient)
		return &auth.DeviceCode{VerificationURL: "https://auth.example.test/device", UserCode: "CODE-1", DeviceAuthID: "d1"}, nil
	}
	services.completeDeviceCode = func(_ context.Context, options *auth.OAuthOptions, _ *auth.DeviceCode) error {
		seen = append(seen, options.HTTPClient)
		return nil
	}
	services.startBrowser = func(_ context.Context, options *auth.OAuthOptions) (*auth.BrowserLoginServer, error) {
		seen = append(seen, options.HTTPClient)
		return nil, nil
	}
	model := newAuthFlowModel(context.Background(), AuthFlowOptions{
		CodexHome:      t.TempDir(),
		ChatGPTAllowed: true,
		APIKeyAllowed:  true,
		HTTPClient:     bound,
		services:       services,
	})
	_, start := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'2'}})
	if start == nil {
		t.Fatal("device start command is nil")
	}
	if _, finish := model.Update(start()); finish != nil {
		model.Update(finish())
	}
	if len(seen) == 0 {
		t.Fatal("the login flow made no request")
	}
	for _, client := range seen {
		if client != bound {
			t.Fatalf("login request client = %#v, want the bound client", client)
		}
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

// Mirrors Rust's `login_can_complete_while_browser_is_opening` (#48502): the
// pending login state is recorded before the browser starts, so a completion
// that arrives while the browser is opening still matches the active login.
func TestAuthFlowBrowserLoginCanCompleteWhileOpening(t *testing.T) {
	done := make(chan error, 1)
	done <- nil
	services := defaultAuthFlowServices()
	services.startBrowser = func(_ context.Context, options *auth.OAuthOptions) (*auth.BrowserLoginServer, error) {
		if !options.OpenBrowser {
			t.Fatal("the local login flow must request browser opening")
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
	if start == nil {
		t.Fatal("browser start command missing")
	}
	// The state must already be recorded before the browser-start command runs.
	if model.state != SignInChatGPTContinueInBrowser {
		t.Fatalf("state before browser start = %s, want %s", model.state, SignInChatGPTContinueInBrowser)
	}
	_, wait := model.Update(start())
	if wait == nil {
		t.Fatal("browser wait command missing")
	}
	// The completion was already queued, so the wait resolves immediately.
	model.Update(wait())
	if model.state != SignInChatGPTSuccessMessage {
		t.Fatalf("state after immediate completion = %s, want %s", model.state, SignInChatGPTSuccessMessage)
	}
}

// Mirrors Rust's `opens_login_browser_for_local_app_servers` (#48502): the
// browser is opened for the interactive ChatGPT choice and not for the device
// code choice, whose one-time code is entered on another device.
func TestAuthFlowBrowserOpeningFollowsTheChoice(t *testing.T) {
	services := defaultAuthFlowServices()
	services.startBrowser = func(_ context.Context, options *auth.OAuthOptions) (*auth.BrowserLoginServer, error) {
		if !options.OpenBrowser {
			t.Fatal("the ChatGPT choice must open the browser")
		}
		return &auth.BrowserLoginServer{AuthURL: "https://auth.example.test/login", Done: make(chan error, 1)}, nil
	}
	services.requestDeviceCode = func(_ context.Context, options *auth.OAuthOptions) (*auth.DeviceCode, error) {
		if options.OpenBrowser {
			t.Fatal("the device code choice must not open the browser")
		}
		return &auth.DeviceCode{VerificationURL: "https://auth.example.test/device", UserCode: "CODE-1", DeviceAuthID: "d1"}, nil
	}
	model := newAuthFlowModel(context.Background(), AuthFlowOptions{
		CodexHome:      t.TempDir(),
		ChatGPTAllowed: true,
		APIKeyAllowed:  true,
		services:       services,
	})
	if _, cmd := model.startChoice(AuthChoiceChatGPT); cmd == nil {
		t.Fatal("ChatGPT start command missing")
	} else {
		cmd()
	}
	if _, cmd := model.startChoice(AuthChoiceDeviceCode); cmd == nil {
		t.Fatal("device code start command missing")
	} else {
		cmd()
	}
}

// Mirrors Rust #48544: the browser sign-in step advertises the `c` copy
// shortcut, copies the sign-in URL with a status line for the result, and clears
// the notice when the attempt is cancelled.
func TestAuthFlowCopiesBrowserLoginURLLikeRust(t *testing.T) {
	done := make(chan error, 1)
	services := defaultAuthFlowServices()
	services.startBrowser = func(context.Context, *auth.OAuthOptions) (*auth.BrowserLoginServer, error) {
		return &auth.BrowserLoginServer{
			AuthURL: "https://auth.example.test/login",
			Done:    done,
		}, nil
	}
	var copied []string
	copyErr := error(nil)
	services.copyText = func(text string) error {
		copied = append(copied, text)
		return copyErr
	}
	model := newAuthFlowModel(context.Background(), AuthFlowOptions{
		CodexHome:      t.TempDir(),
		ChatGPTAllowed: true,
		APIKeyAllowed:  true,
		services:       services,
	})

	_, start := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	// The URL is not known yet, so the shortcut is inert.
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'c'}})
	if len(copied) != 0 {
		t.Fatalf("copied before the URL arrived: %#v", copied)
	}
	model.Update(start())
	if view := model.View(); !strings.Contains(view, "press c to copy it:") {
		t.Fatalf("browser view missing the copy hint:\n%s", view)
	}

	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'c'}})
	if len(copied) != 1 || copied[0] != "https://auth.example.test/login" {
		t.Fatalf("copied = %#v, want the sign-in URL", copied)
	}
	if view := model.View(); !strings.Contains(view, "Copied link to clipboard") {
		t.Fatalf("browser view missing the copy result:\n%s", view)
	}

	copyErr = errors.New("clipboard offline")
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'c'}})
	if view := model.View(); !strings.Contains(view, "Could not copy link: clipboard offline") {
		t.Fatalf("browser view missing the copy failure:\n%s", view)
	}

	// Esc cancels the attempt and clears the notice.
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	if model.state != SignInPickMode {
		t.Fatalf("state after esc = %s, want the picker", model.state)
	}
	if view := model.View(); strings.Contains(view, "Copied link") || strings.Contains(view, "Could not copy link") {
		t.Fatalf("copy notice survived the cancel:\n%s", view)
	}
}
