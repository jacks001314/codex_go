package onboarding

import (
	"context"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/auth"
)

func bedrockKeyRune(value rune) bubbletea.KeyMsg {
	return bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{value}}
}

func bedrockKeyType(value string) bubbletea.KeyMsg {
	return bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune(value)}
}

func bedrockTestState(profiles []appserver.BedrockAwsProfile, environment []appserver.BedrockEnvironmentCredential) *bedrockState {
	state := newBedrockStateDiscovered(&appserver.BedrockDiscoverResponse{
		Profiles:               profiles,
		EnvironmentCredentials: environment,
	})
	return state
}

func TestAuthOptionsIncludeBedrockOnlyWithAPIAndFeature(t *testing.T) {
	options := AuthOptionsForPolicyWithBedrock(true, true, true)
	if len(options) != 4 || options[3].Choice != AuthChoiceBedrock {
		t.Fatalf("bedrock options = %#v", options)
	}
	if options := AuthOptionsForPolicyWithBedrock(true, false, true); len(options) != 2 {
		t.Fatalf("bedrock must require API login: %#v", options)
	}
	if options := AuthOptionsForPolicyWithBedrock(true, true, false); len(options) != 3 {
		t.Fatalf("disabled bedrock leaked into options: %#v", options)
	}
}

func TestBedrockProfileFlowConfiguresAndCompletes(t *testing.T) {
	region := "us-east-2"
	var setup appserver.BedrockSetupParams
	model := newAuthFlowModel(context.Background(), AuthFlowOptions{
		ChatGPTAllowed: true,
		APIKeyAllowed:  true,
		BedrockAllowed: true,
		BedrockDiscover: func(context.Context) (*appserver.BedrockDiscoverResponse, error) {
			return &appserver.BedrockDiscoverResponse{
				Profiles: []appserver.BedrockAwsProfile{{Name: "engineering", Region: &region}},
			}, nil
		},
		BedrockSetup: func(_ context.Context, params appserver.BedrockSetupParams) error {
			setup = params
			return nil
		},
		BedrockCheckGovCloud: func(context.Context) (bool, error) { return false, nil },
	})
	model.highlighted = AuthChoiceBedrock
	updated, command := model.startChoice(AuthChoiceBedrock)
	model = updated.(*authFlowModel)
	if model.state != SignInBedrock || command == nil {
		t.Fatalf("bedrock start state=%s cmd=%v", model.state, command)
	}
	model.Update(command())
	if model.bedrock == nil || model.bedrock.view != bedrockViewMethodsDetected {
		t.Fatalf("discovery state = %#v", model.bedrock)
	}
	updated, command = model.handleBedrockKey(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*authFlowModel)
	if command == nil || model.bedrock.view != bedrockViewConfiguring {
		t.Fatalf("profile selection state = %#v cmd=%v", model.bedrock, command)
	}
	updated, command = model.Update(command())
	model = updated.(*authFlowModel)
	if command == nil {
		t.Fatalf("GovCloud check command missing")
	}
	updated, _ = model.Update(command())
	model = updated.(*authFlowModel)
	if !model.completed || model.state != SignInBedrockConfigured {
		t.Fatalf("completion state = %s completed=%v", model.state, model.completed)
	}
	if setup.Type != appserver.BedrockSetupTypeProfile || setup.Profile != "engineering" || setup.Region != region {
		t.Fatalf("setup params = %#v", setup)
	}
}

func TestBedrockAccessKeyFlowUsesAccessKeyLogin(t *testing.T) {
	var login auth.LoginAccountParams
	model := newAuthFlowModel(context.Background(), AuthFlowOptions{
		ChatGPTAllowed: true,
		APIKeyAllowed:  true,
		BedrockAllowed: true,
		BedrockDiscover: func(context.Context) (*appserver.BedrockDiscoverResponse, error) {
			return &appserver.BedrockDiscoverResponse{}, nil
		},
		BedrockLoginAccessKeys: func(_ context.Context, params auth.LoginAccountParams) error {
			login = params
			return nil
		},
	})
	model.bedrock = bedrockTestState(nil, nil)
	model.state = SignInBedrock
	// Manual profile -> access keys.
	model.handleBedrockKey(bubbletea.KeyMsg{Type: bubbletea.KeyDown})
	updated, _ := model.handleBedrockKey(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*authFlowModel)
	if model.bedrock.view != bedrockViewAccessKeyEntry {
		t.Fatalf("access key view = %v", model.bedrock.view)
	}
	model.handleBedrockKey(bedrockKeyType("AKID"))
	model.handleBedrockKey(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model.handleBedrockKey(bedrockKeyType("SECRET"))
	model.handleBedrockKey(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model.handleBedrockKey(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model.handleBedrockKey(bedrockKeyType("us-west-2"))
	updated, command := model.handleBedrockKey(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*authFlowModel)
	if command == nil {
		t.Fatal("access-key configuration returned no command")
	}
	model.Update(command())
	if login.Type != "amazonBedrockAccessKeys" || login.AccessKeyID != "AKID" ||
		login.SecretAccessKey != "SECRET" || login.Region != "us-west-2" {
		t.Fatalf("login params = %#v", login)
	}
}

func TestBedrockAPIKeyFlowAndGovCloudGuidance(t *testing.T) {
	var apiKey, region string
	model := newAuthFlowModel(context.Background(), AuthFlowOptions{
		ChatGPTAllowed: true,
		APIKeyAllowed:  true,
		BedrockAllowed: true,
		BedrockDiscover: func(context.Context) (*appserver.BedrockDiscoverResponse, error) {
			return &appserver.BedrockDiscoverResponse{}, nil
		},
		BedrockLoginAPIKey: func(_ context.Context, key string, selectedRegion string) error {
			apiKey, region = key, selectedRegion
			return nil
		},
		BedrockCheckGovCloud: func(context.Context) (bool, error) { return true, nil },
	})
	model.bedrock = bedrockTestState(nil, nil)
	model.state = SignInBedrock
	for range 3 {
		model.handleBedrockKey(bubbletea.KeyMsg{Type: bubbletea.KeyDown})
	}
	updated, _ := model.handleBedrockKey(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*authFlowModel)
	if model.bedrock.view != bedrockViewAPIKeyEntry {
		t.Fatalf("api key view = %v", model.bedrock.view)
	}
	model.handleBedrockKey(bedrockKeyType("bedrock-key"))
	model.handleBedrockKey(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model.handleBedrockKey(bedrockKeyType("us-gov-west-1"))
	updated, command := model.handleBedrockKey(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*authFlowModel)
	updated, govCommand := model.Update(command())
	model = updated.(*authFlowModel)
	if apiKey != "bedrock-key" || region != "us-gov-west-1" {
		t.Fatalf("api key login = %q / %q", apiKey, region)
	}
	if govCommand == nil {
		t.Fatal("GovCloud command missing")
	}
	updated, _ = model.Update(govCommand())
	model = updated.(*authFlowModel)
	if model.bedrock.view != bedrockViewGovCloudWarning {
		t.Fatalf("GovCloud view = %v", model.bedrock.view)
	}
	if !strings.Contains(model.View(), "Using Codex with AWS GovCloud") {
		t.Fatalf("guidance render:\n%s", model.View())
	}
	updated, command = model.handleBedrockKey(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*authFlowModel)
	if !model.completed || command == nil {
		t.Fatalf("guidance acknowledge state=%s cmd=%v", model.state, command)
	}
}
