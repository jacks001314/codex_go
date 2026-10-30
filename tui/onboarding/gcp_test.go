package onboarding

import (
	"context"
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
)

// AuthOptionsIncludeGcpOnlyWithAPIAndFeature pins Rust #57857: the Google Cloud
// Vertex AI wizard is offered only when API login and its feature flag are both
// allowed, and it follows the Bedrock entry.
func TestAuthOptionsIncludeGcpOnlyWithAPIAndFeature(t *testing.T) {
	options := AuthOptionsForPolicyWithProviders(true, true, true, true)
	if len(options) != 5 || options[3].Choice != AuthChoiceBedrock || options[4].Choice != AuthChoiceGCP {
		t.Fatalf("provider options = %#v", options)
	}
	if options := AuthOptionsForPolicyWithProviders(true, false, true, true); len(options) != 2 {
		t.Fatalf("gcp must require API login: %#v", options)
	}
	if options := AuthOptionsForPolicyWithProviders(true, true, false, false); len(options) != 3 {
		t.Fatalf("disabled gcp leaked into options: %#v", options)
	}
	// The Bedrock-only constructor must not offer GCP.
	if options := AuthOptionsForPolicyWithBedrock(true, true, true); len(options) != 4 {
		t.Fatalf("bedrock-only options leaked gcp: %#v", options)
	}
}

func gcpTestModel(setup func(context.Context, appserver.GcpSetupParams) error) *authFlowModel {
	model := newAuthFlowModel(context.Background(), AuthFlowOptions{
		ChatGPTAllowed: true,
		APIKeyAllowed:  true,
		GcpAllowed:     true,
		GcpSetup:       setup,
	})
	model.highlighted = AuthChoiceGCP
	updated, _ := model.startChoice(AuthChoiceGCP)
	return updated.(*authFlowModel)
}

// TestGcpSetupFlowCollectsFieldsAndCompletesLikeRust drives the whole wizard:
// project ID, location and credential file are collected and forwarded to the
// app-server setup request, then the flow completes.
func TestGcpSetupFlowCollectsFieldsAndCompletesLikeRust(t *testing.T) {
	var got appserver.GcpSetupParams
	model := gcpTestModel(func(_ context.Context, params appserver.GcpSetupParams) error {
		got = params
		return nil
	})
	if model.state != SignInGCP || model.gcp == nil || model.gcp.view != gcpViewProject {
		t.Fatalf("gcp start state=%s view=%#v", model.state, model.gcp)
	}
	if !strings.Contains(model.View(), "Set up Google Cloud Vertex AI") ||
		!strings.Contains(model.View(), "Project ID") {
		t.Fatalf("project render:\n%s", model.View())
	}

	model.handleGCPKey(bedrockKeyType("my-project"))
	updated, _ := model.handleGCPKey(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*authFlowModel)
	if model.gcp.view != gcpViewLocation {
		t.Fatalf("project confirm view = %v", model.gcp.view)
	}
	model.handleGCPKey(bedrockKeyType("us-central1"))
	updated, _ = model.handleGCPKey(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*authFlowModel)
	if model.gcp.view != gcpViewCredentials {
		t.Fatalf("location confirm view = %v", model.gcp.view)
	}
	model.handleGCPKey(bedrockKeyType("/tmp/adc.json"))
	updated, command := model.handleGCPKey(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*authFlowModel)
	if model.gcp.view != gcpViewConfiguring {
		t.Fatalf("credentials confirm view = %v", model.gcp.view)
	}
	if command == nil {
		t.Fatal("credentials confirm returned no setup command")
	}
	updated, _ = model.Update(command())
	model = updated.(*authFlowModel)
	if !model.completed || model.state != SignInGCPConfigured {
		t.Fatalf("completion state=%s completed=%v", model.state, model.completed)
	}
	if got.ProjectID != "my-project" {
		t.Fatalf("project id = %q", got.ProjectID)
	}
	if got.Location == nil || *got.Location != "us-central1" {
		t.Fatalf("location = %#v", got.Location)
	}
	if got.CredentialFile == nil || *got.CredentialFile != "/tmp/adc.json" {
		t.Fatalf("credential file = %#v", got.CredentialFile)
	}
}

// TestGcpSetupRequiresProjectIDLikeRust keeps the wizard on the project step
// and surfaces the required-field error until a project ID is entered.
func TestGcpSetupRequiresProjectIDLikeRust(t *testing.T) {
	model := gcpTestModel(func(context.Context, appserver.GcpSetupParams) error { return nil })
	updated, command := model.handleGCPKey(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*authFlowModel)
	if command != nil {
		t.Fatal("empty project id dispatched a setup command")
	}
	if model.gcp.view != gcpViewProject {
		t.Fatalf("view advanced past project: %v", model.gcp.view)
	}
	if !strings.Contains(model.View(), "Enter a Google Cloud project ID.") {
		t.Fatalf("missing project error:\n%s", model.View())
	}
}

// TestGcpSetupDefaultsToGlobalAndAdcLikeRust sends nil location and credential
// pointers when the optional fields are left blank.
func TestGcpSetupDefaultsToGlobalAndAdcLikeRust(t *testing.T) {
	var got appserver.GcpSetupParams
	model := gcpTestModel(func(_ context.Context, params appserver.GcpSetupParams) error {
		got = params
		return nil
	})
	model.handleGCPKey(bedrockKeyType("proj"))
	updated, _ := model.handleGCPKey(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*authFlowModel)
	updated, _ = model.handleGCPKey(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*authFlowModel)
	updated, command := model.handleGCPKey(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*authFlowModel)
	model.Update(command())
	if got.Location != nil || got.CredentialFile != nil {
		t.Fatalf("blank optional fields = %#v", got)
	}
}

// TestGcpCancelReturnsToProviderMenuLikeRust covers esc from the project step.
func TestGcpCancelReturnsToProviderMenuLikeRust(t *testing.T) {
	model := gcpTestModel(func(context.Context, appserver.GcpSetupParams) error { return nil })
	updated, _ := model.handleGCPKey(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	model = updated.(*authFlowModel)
	if model.state != SignInPickMode || model.gcp != nil {
		t.Fatalf("cancel state=%s gcp=%#v", model.state, model.gcp)
	}
}

// TestGcpSetupErrorReturnsToCredentialsLikeRust restores the credential step
// with the server error when setup fails.
func TestGcpSetupErrorReturnsToCredentialsLikeRust(t *testing.T) {
	model := gcpTestModel(func(context.Context, appserver.GcpSetupParams) error {
		return errors.New("credential file invalid")
	})
	model.gcp.projectID = "proj"
	model.gcp.view = gcpViewCredentials
	updated, command := model.handleGCPKey(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*authFlowModel)
	updated, _ = model.Update(command())
	model = updated.(*authFlowModel)
	if model.completed || model.state != SignInGCP {
		t.Fatalf("error completion state=%s completed=%v", model.state, model.completed)
	}
	if model.gcp.view != gcpViewCredentials {
		t.Fatalf("error view = %v, want credentials", model.gcp.view)
	}
	if !strings.Contains(model.View(), "credential file invalid") {
		t.Fatalf("setup error missing:\n%s", model.View())
	}
}
