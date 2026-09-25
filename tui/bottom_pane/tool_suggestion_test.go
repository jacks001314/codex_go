package bottompane

import "testing"

// Rust parity: codex-rs/tui/src/bottom_pane/mcp_server_elicitation.rs
// parse_tool_suggestion_request and the tool-suggestion branch of
// push_mcp_server_elicitation_request (#48015).

func toolSuggestionMeta(overrides map[string]any) map[string]any {
	meta := map[string]any{
		"codex_approval_kind": "tool_suggestion",
		"tool_type":           "connector",
		"suggest_type":        "install",
		"suggest_reason":      "Install the connector to continue",
		"tool_id":             "connector_test",
		"tool_name":           "Test Connector",
	}
	for key, value := range overrides {
		if value == nil {
			delete(meta, key)
			continue
		}
		meta[key] = value
	}
	return meta
}

func TestParseToolSuggestionRequestMatchesRust(t *testing.T) {
	suggestion := parseToolSuggestionRequest(toolSuggestionMeta(map[string]any{"install_url": "https://example.test/install"}))
	if suggestion == nil {
		t.Fatal("parseToolSuggestionRequest() = nil, want the connector suggestion")
	}
	if suggestion.ToolType != ToolSuggestionToolConnector || suggestion.SuggestType != ToolSuggestionInstall {
		t.Fatalf("suggestion = %#v", suggestion)
	}
	if suggestion.SuggestReason != "Install the connector to continue" ||
		suggestion.ToolID != "connector_test" || suggestion.ToolName != "Test Connector" {
		t.Fatalf("suggestion fields = %#v", suggestion)
	}
	if suggestion.InstallURL == nil || *suggestion.InstallURL != "https://example.test/install" {
		t.Fatalf("install url = %#v", suggestion.InstallURL)
	}

	plugin := parseToolSuggestionRequest(toolSuggestionMeta(map[string]any{"tool_type": "plugin", "suggest_type": "enable"}))
	if plugin == nil || plugin.ToolType != ToolSuggestionToolPlugin || plugin.SuggestType != ToolSuggestionEnable {
		t.Fatalf("plugin suggestion = %#v", plugin)
	}
	if plugin.InstallURL != nil {
		t.Fatalf("install url without the meta key = %#v, want nil", plugin.InstallURL)
	}

	// Any unrecognized or missing required field means the elicitation is not a
	// tool suggestion (Rust's parse returns None).
	for name, meta := range map[string]map[string]any{
		"no meta":             nil,
		"other approval kind": toolSuggestionMeta(map[string]any{"codex_approval_kind": "mcp_tool_call"}),
		"unknown tool type":   toolSuggestionMeta(map[string]any{"tool_type": "widget"}),
		"unknown suggest":     toolSuggestionMeta(map[string]any{"suggest_type": "uninstall"}),
		"missing reason":      toolSuggestionMeta(map[string]any{"suggest_reason": nil}),
		"missing tool id":     toolSuggestionMeta(map[string]any{"tool_id": nil}),
		"missing tool name":   toolSuggestionMeta(map[string]any{"tool_name": nil}),
	} {
		t.Run(name, func(t *testing.T) {
			if suggestion := parseToolSuggestionRequest(meta); suggestion != nil {
				t.Fatalf("parseToolSuggestionRequest() = %#v, want nil", suggestion)
			}
		})
	}

	// The form request exposes the parsed suggestion.
	form, err := NewElicitationFormRequest("connector-server", "9", "Install Test Connector", map[string]any{"type": "object", "properties": map[string]any{}}, toolSuggestionMeta(map[string]any{"install_url": "https://example.test/install"}))
	if err != nil {
		t.Fatalf("NewElicitationFormRequest() error = %v", err)
	}
	if form.ToolSuggestion() == nil || form.ToolSuggestion().ToolID != "connector_test" {
		t.Fatalf("form tool suggestion = %#v", form.ToolSuggestion())
	}
	plain, err := NewElicitationFormRequest("server", "10", "message", map[string]any{"type": "object", "properties": map[string]any{}}, nil)
	if err != nil {
		t.Fatalf("NewElicitationFormRequest(plain) error = %v", err)
	}
	if plain.ToolSuggestion() != nil {
		t.Fatalf("plain form tool suggestion = %#v, want nil", plain.ToolSuggestion())
	}
}

// TestAppLinkParamsFromToolSuggestionValidatesURLsLikeRust mirrors Rust #48015:
// malformed URLs, unsupported schemes and embedded credentials are declined
// instead of reaching the app link view, and only credential-free HTTPS URLs
// with a host are shown.
func TestAppLinkParamsFromToolSuggestionValidatesURLsLikeRust(t *testing.T) {
	target := AppLinkElicitationTarget{ThreadID: "thread-1", ServerName: "connector-server", RequestID: "9"}
	for _, installURL := range []string{
		"file:///tmp/connector",
		"http://example.test/install",
		"custom://example.test/install",
		"not a URL",
		"https://user:password@example.test/install",
	} {
		t.Run(installURL, func(t *testing.T) {
			suggestion := parseToolSuggestionRequest(toolSuggestionMeta(map[string]any{"install_url": installURL}))
			if suggestion == nil {
				t.Fatalf("suggestion for %q = nil", installURL)
			}
			params, outcome := AppLinkParamsFromToolSuggestion(suggestion, target)
			if outcome != AppLinkToolSuggestionDecline {
				t.Fatalf("outcome for %q = %q, want decline", installURL, outcome)
			}
			if params != (AppLinkViewParams{}) {
				t.Fatalf("params for %q = %#v, want none", installURL, params)
			}
		})
	}

	// A suggestion without an install URL keeps the plain form.
	withoutURL := parseToolSuggestionRequest(toolSuggestionMeta(nil))
	if _, outcome := AppLinkParamsFromToolSuggestion(withoutURL, target); outcome != AppLinkToolSuggestionForm {
		t.Fatalf("outcome without an install url = %q, want form", outcome)
	}
	if _, outcome := AppLinkParamsFromToolSuggestion(nil, target); outcome != AppLinkToolSuggestionForm {
		t.Fatalf("outcome without a suggestion = %q, want form", outcome)
	}

	// A valid URL produces Rust's install params.
	suggestion := parseToolSuggestionRequest(toolSuggestionMeta(map[string]any{"install_url": "https://example.test/install"}))
	params, outcome := AppLinkParamsFromToolSuggestion(suggestion, target)
	if outcome != AppLinkToolSuggestionAppLink {
		t.Fatalf("outcome for a valid url = %q, want app_link", outcome)
	}
	if params.AppID != "connector_test" || params.Title != "Test Connector" || params.URL != "https://example.test/install" {
		t.Fatalf("params = %#v", params)
	}
	if params.IsInstalled || params.IsEnabled {
		t.Fatalf("install params installed/enabled = %v/%v, want false/false", params.IsInstalled, params.IsEnabled)
	}
	if params.SuggestionType == nil || *params.SuggestionType != AppLinkSuggestionInstall {
		t.Fatalf("suggestion type = %#v", params.SuggestionType)
	}
	if params.Instructions != "Install this app in your browser, then return here." {
		t.Fatalf("instructions = %q", params.Instructions)
	}
	if params.SuggestReason == nil || *params.SuggestReason != "Install the connector to continue" {
		t.Fatalf("suggest reason = %#v", params.SuggestReason)
	}
	if params.ElicitationTarget == nil || *params.ElicitationTarget != target {
		t.Fatalf("elicitation target = %#v, want %#v", params.ElicitationTarget, target)
	}
	view := NewAppLinkView(params)
	if !view.Ready() || view.Screen != AppLinkScreenLink || !view.isToolSuggestion() {
		t.Fatalf("view = %#v", view)
	}

	// Enable suggestions are installed-but-disabled and carry their own text.
	enable := parseToolSuggestionRequest(toolSuggestionMeta(map[string]any{"suggest_type": "enable", "install_url": "https://example.test/enable"}))
	enableParams, outcome := AppLinkParamsFromToolSuggestion(enable, target)
	if outcome != AppLinkToolSuggestionAppLink {
		t.Fatalf("enable outcome = %q, want app_link", outcome)
	}
	if !enableParams.IsInstalled || enableParams.IsEnabled {
		t.Fatalf("enable params installed/enabled = %v/%v, want true/false", enableParams.IsInstalled, enableParams.IsEnabled)
	}
	if enableParams.SuggestionType == nil || *enableParams.SuggestionType != AppLinkSuggestionEnable {
		t.Fatalf("enable suggestion type = %#v", enableParams.SuggestionType)
	}
	if enableParams.Instructions != "Enable this app to use it for the current request." {
		t.Fatalf("enable instructions = %q", enableParams.Instructions)
	}
}
