package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codex_go/plugin"
)

func TestListAvailablePluginsToInstallHandler(t *testing.T) {
	longDescription := strings.Repeat("x", maxPluginInstallCandidateDescriptionChars+1)
	handler := NewListAvailablePluginsToInstallHandler([]plugin.DiscoverableInfo{
		{ID: "sample@market", Name: "Sample", Description: longDescription, HasSkills: true, MCPServerNames: []string{"sample-mcp"}},
		{ID: "calendar@market", Name: "Calendar", Description: "calendar"},
		{ID: "connector_docs", Name: "Docs Connector", Description: "docs", ToolType: "connector", InstallURL: "https://chatgpt.com/apps/docs", PluginDisplayNames: []string{"Docs Plugin"}},
	})
	output, err := handler.Execute(context.Background(), &Invocation{Payload: Payload{Kind: PayloadFunction, Arguments: "{}"}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var result ListAvailablePluginsToInstallResult
	if err := json.Unmarshal([]byte(output.Body), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(result.Tools) != 3 || result.Tools[0].ID != "calendar@market" || result.Tools[1].ID != "connector_docs" || result.Tools[2].ID != "sample@market" {
		t.Fatalf("tools = %#v", result.Tools)
	}
	if result.Tools[1].ToolType != "connector" {
		t.Fatalf("connector tool_type = %q", result.Tools[1].ToolType)
	}
	if result.Tools[1].InstallURL == nil || *result.Tools[1].InstallURL != "https://chatgpt.com/apps/docs" {
		t.Fatalf("connector install_url = %#v", result.Tools[1].InstallURL)
	}
	if strings.Join(result.Tools[1].PluginDisplayNames, ",") != "Docs Plugin" {
		t.Fatalf("connector plugin_display_names = %#v", result.Tools[1].PluginDisplayNames)
	}
	if result.Tools[2].Description == nil || len([]rune(*result.Tools[2].Description)) != maxPluginInstallCandidateDescriptionChars {
		t.Fatalf("description was not truncated: %#v", result.Tools[2].Description)
	}
}

func TestRequestPluginInstallHandlerRecommendationContext(t *testing.T) {
	handler := NewRequestPluginInstallHandler(&RequestPluginInstallHandlerOptions{
		Candidates:            []plugin.DiscoverableInfo{{ID: "docs@market", Name: "Docs"}},
		RecommendationContext: true,
	})
	spec := handler.Spec()
	if !strings.Contains(spec.Description, "<recommended_plugins>") {
		t.Fatalf("spec description = %q", spec.Description)
	}
	if spec.Parallel {
		t.Fatalf("request_plugin_install should not be parallel")
	}
	output, err := handler.Execute(context.Background(), &Invocation{Payload: Payload{Kind: PayloadFunction, Arguments: `{"plugin_id":"docs@market","suggest_reason":"Useful docs"}`}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var result RequestPluginInstallResult
	if err := json.Unmarshal([]byte(output.Body), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result.Completed || result.UserConfirmed || result.ToolType != "plugin" || result.ActionType != "install" || result.ToolID != "docs@market" || result.ToolName != "Docs" {
		t.Fatalf("result = %#v", result)
	}
	if _, err := handler.Execute(context.Background(), &Invocation{Payload: Payload{Kind: PayloadFunction, Arguments: `{"plugin_id":"missing","suggest_reason":"Useful"}`}}); err == nil {
		t.Fatalf("Execute(missing) returned nil error")
	}
}

// TestRequestPluginInstallHandlerRejectsNonRootAgentLikeRust mirrors Rust
// #45806: a subagent's install request is rejected with the root-only guidance
// before the arguments are parsed, and no installation elicitation is emitted.
func TestRequestPluginInstallHandlerRejectsNonRootAgentLikeRust(t *testing.T) {
	candidates := []plugin.DiscoverableInfo{{
		ID:              "docs@market",
		RemotePluginID:  "plugins~Docs",
		Name:            "Docs",
		AppConnectorIDs: []string{"connector_docs"},
	}}
	for _, recommendation := range []bool{false, true} {
		name := "legacy install request"
		arguments := `{"tool_type":"plugin","action_type":"install","tool_id":"docs@market","suggest_reason":"Use docs"}`
		if recommendation {
			name = "recommended plugin request"
			arguments = `{"plugin_id":"docs@market","suggest_reason":"Use docs"}`
		}
		t.Run(name, func(t *testing.T) {
			runtime := &fakePluginInstallRuntime{result: &PluginInstallRuntimeResult{Sent: true, UserConfirmed: true, Completed: true}}
			handler := NewRequestPluginInstallHandler(&RequestPluginInstallHandlerOptions{
				Candidates:            candidates,
				RecommendationContext: recommendation,
				Runtime:               runtime,
				NonRootAgent:          true,
			})
			_, err := handler.Execute(context.Background(), &Invocation{
				CallID:  "install-subagent",
				Payload: Payload{Kind: PayloadFunction, Arguments: arguments},
			})
			var callErr *FunctionCallError
			if !AsFunctionCallError(err, &callErr) || callErr.ModelMessage() != "request_plugin_install can only be used by the root thread" {
				t.Fatalf("error = %#v", err)
			}
			if runtime.request != nil {
				t.Fatalf("subagent call emitted an installation request: %#v", runtime.request)
			}
			// Malformed arguments are not parsed either: the guard runs first.
			_, err = handler.Execute(context.Background(), &Invocation{
				CallID:  "install-subagent-bad-args",
				Payload: Payload{Kind: PayloadFunction, Arguments: `{"not":"valid"}`},
			})
			if !AsFunctionCallError(err, &callErr) || callErr.ModelMessage() != "request_plugin_install can only be used by the root thread" {
				t.Fatalf("malformed-argument error = %#v", err)
			}
		})
	}
}

func TestRequestPluginInstallHandlerUsesRuntimeResult(t *testing.T) {
	runtime := &fakePluginInstallRuntime{result: &PluginInstallRuntimeResult{Sent: true, UserConfirmed: true, Completed: true, ResponseAction: "accept"}}
	handler := NewRequestPluginInstallHandler(&RequestPluginInstallHandlerOptions{
		Candidates: []plugin.DiscoverableInfo{{
			ID:              "docs@market",
			RemotePluginID:  "plugins~Docs",
			Name:            "Docs",
			AppConnectorIDs: []string{"connector_docs"},
		}},
		RecommendationContext: true,
		Runtime:               runtime,
	})
	output, err := handler.Execute(context.Background(), &Invocation{
		CallID:  "install-docs",
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"plugin_id":"docs@market","suggest_reason":"Use docs"}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var result RequestPluginInstallResult
	if err := json.Unmarshal([]byte(output.Body), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !result.Completed || !result.UserConfirmed || result.ResponseAction != "accept" || !result.Elicitation || result.SuggestionID != "request_plugin_install_install-docs" {
		t.Fatalf("result = %#v", result)
	}
	if runtime.request == nil || runtime.request.SuggestionID != "request_plugin_install_install-docs" {
		t.Fatalf("runtime request = %#v", runtime.request)
	}
	if runtime.request.Elicitation == nil || runtime.request.Elicitation.Meta["remote_plugin_id"] != "plugins~Docs" {
		t.Fatalf("elicitation = %#v", runtime.request.Elicitation)
	}
	if runtime.request.Elicitation.Meta["suggestion_id"] != "request_plugin_install_install-docs" {
		t.Fatalf("plugin elicitation suggestion_id = %#v", runtime.request.Elicitation.Meta["suggestion_id"])
	}
}

func TestRequestPluginInstallHandlerReturnsDeclineMetadata(t *testing.T) {
	runtime := &fakePluginInstallRuntime{result: &PluginInstallRuntimeResult{Sent: true, ResponseAction: "decline", PersistDisable: true}}
	handler := NewRequestPluginInstallHandler(&RequestPluginInstallHandlerOptions{
		Candidates: []plugin.DiscoverableInfo{{ID: "docs@market", Name: "Docs"}},
		Runtime:    runtime,
	})
	output, err := handler.Execute(context.Background(), &Invocation{
		CallID:  "decline-docs",
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"tool_type":"plugin","action_type":"install","tool_id":"docs@market","suggest_reason":"Use docs"}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var result RequestPluginInstallResult
	if err := json.Unmarshal([]byte(output.Body), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result.UserConfirmed || result.Completed || result.ResponseAction != "decline" || !result.PersistDisable || !result.Elicitation {
		t.Fatalf("result = %#v", result)
	}
	if output.Data["response_action"] != "decline" || output.Data["persist_disable"] != true {
		t.Fatalf("output data = %#v", output.Data)
	}
}

func TestRequestPluginInstallHandlerBlocksTUIPluginInstallWithoutToolType(t *testing.T) {
	runtime := &fakePluginInstallRuntime{result: &PluginInstallRuntimeResult{Sent: true, UserConfirmed: true, Completed: true}}
	handler := NewRequestPluginInstallHandler(&RequestPluginInstallHandlerOptions{
		Candidates:          []plugin.DiscoverableInfo{{ID: "docs@market", Name: "Docs"}},
		Runtime:             runtime,
		AppServerClientName: requestPluginInstallTUIClientName,
	})
	_, err := handler.Execute(context.Background(), &Invocation{
		CallID:  "install-docs",
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"action_type":"install","tool_id":"docs@market","suggest_reason":"Use docs"}`},
	})
	if err == nil || !strings.Contains(err.Error(), "not available in codex-tui") {
		t.Fatalf("Execute() error = %v, want codex-tui plugin install block", err)
	}
	if runtime.request != nil {
		t.Fatalf("runtime request = %#v, want nil", runtime.request)
	}
}

func TestRequestPluginInstallHandlerSupportsConnectorCandidates(t *testing.T) {
	runtime := &fakePluginInstallRuntime{result: &PluginInstallRuntimeResult{Sent: true, UserConfirmed: true, ResponseAction: "accept"}}
	handler := NewRequestPluginInstallHandler(&RequestPluginInstallHandlerOptions{
		Candidates: []plugin.DiscoverableInfo{{
			ID:                 "connector_docs",
			Name:               "Docs Connector",
			ToolType:           "connector",
			InstallURL:         "https://chatgpt.com/apps/docs",
			PluginDisplayNames: []string{"Docs Plugin"},
		}},
		Runtime: runtime,
	})
	if spec := handler.Spec(); spec.Parallel {
		t.Fatalf("connector request_plugin_install should not be parallel")
	}
	output, err := handler.Execute(context.Background(), &Invocation{
		CallID:  "install-connector",
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"tool_type":"connector","action_type":"install","tool_id":"connector_docs","suggest_reason":"Connect docs"}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var result RequestPluginInstallResult
	if err := json.Unmarshal([]byte(output.Body), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result.ToolType != "connector" || !result.UserConfirmed || result.Completed {
		t.Fatalf("result = %#v", result)
	}
	if runtime.request == nil || runtime.request.ToolType != "connector" {
		t.Fatalf("runtime request = %#v", runtime.request)
	}
	if runtime.request.Elicitation.Meta["install_url"] != "https://chatgpt.com/apps/docs" {
		t.Fatalf("elicitation meta = %#v", runtime.request.Elicitation.Meta)
	}
	if _, present := runtime.request.Elicitation.Meta["suggestion_id"]; present {
		t.Fatalf("connector elicitation must omit suggestion_id: %#v", runtime.request.Elicitation.Meta)
	}
	pluginDisplayNames, ok := runtime.request.Elicitation.Meta["plugin_display_names"].([]string)
	if !ok || strings.Join(pluginDisplayNames, ",") != "Docs Plugin" {
		t.Fatalf("elicitation plugin_display_names = %#v", runtime.request.Elicitation.Meta["plugin_display_names"])
	}
}

type fakePluginInstallRuntime struct {
	request *PluginInstallRequest
	result  *PluginInstallRuntimeResult
}

func (r *fakePluginInstallRuntime) RequestPluginInstall(ctx context.Context, request *PluginInstallRequest) (*PluginInstallRuntimeResult, error) {
	_ = ctx
	r.request = request
	return r.result, nil
}
