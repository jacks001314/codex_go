package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	bottompane "codex_go/tui/bottom_pane"
)

// Rust parity: codex-rs/tui/src/chatwidget/tests/app_server.rs
// tool_suggestion_install_url_is_validated_before_opening (#48015): a tool
// suggestion whose install URL is not a credential-free HTTPS URL is declined
// on the requesting thread without showing a popup.
func TestModelDeclinesInvalidToolSuggestionInstallURLsWithoutPopup(t *testing.T) {
	for _, installURL := range []string{
		"file:///tmp/connector",
		"http://example.test/install",
		"custom://example.test/install",
		"not a URL",
		"https://user:password@example.test/install",
	} {
		t.Run(installURL, func(t *testing.T) {
			var responses []ModalResponse
			model := NewModel(nil, Options{
				OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
					responses = append(responses, response)
					return nil
				},
			})
			model.Update(ElicitationRequestMsg{
				ID:         "9",
				ServerName: "connector-server",
				ThreadID:   "requesting-thread",
				TurnID:     "turn-install",
				Message:    "Install Test Connector",
				RequestedSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
				Meta: map[string]any{
					"codex_approval_kind": "tool_suggestion",
					"tool_type":           "connector",
					"suggest_type":        "install",
					"suggest_reason":      "Install the connector to continue",
					"tool_id":             "connector_test",
					"tool_name":           "Test Connector",
					"install_url":         installURL,
				},
			})
			if model.modal != nil {
				t.Fatalf("modal = %#v, want no popup for %q", model.modal, installURL)
			}
			view := model.View()
			if strings.Contains(view, "Test Connector") {
				t.Fatalf("view still shows the suggestion for %q:\n%s", installURL, view)
			}
			if len(responses) != 1 {
				t.Fatalf("responses = %#v, want one decline", responses)
			}
			response := responses[0]
			if response.ID != "9" || response.Kind != ModalKindElicitation || response.Cancelled {
				t.Fatalf("response = %#v", response)
			}
			if response.Elicitation == nil || response.Elicitation.Action != "decline" {
				t.Fatalf("elicitation decision = %#v, want decline", response.Elicitation)
			}
		})
	}
}

// TestModelShowsValidToolSuggestionAppLinkPopupLikeRust mirrors the valid-URL
// arm of Rust's tool_suggestion_install_url_is_validated_before_opening: the
// suggestion renders the app link popup, opening the URL is a host action that
// keeps the confirmation screen open, and confirming resolves the elicitation.
func TestModelShowsValidToolSuggestionAppLinkPopupLikeRust(t *testing.T) {
	var responses []ModalResponse
	var actions []AppLinkAction
	model := NewModel(nil, Options{
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
		OnAppLinkAction: func(action AppLinkAction) bubbletea.Cmd {
			actions = append(actions, action)
			return nil
		},
	})
	model.Update(validToolSuggestionRequest("10"))
	if len(responses) != 0 {
		t.Fatalf("responses = %#v, want the elicitation to stay pending", responses)
	}
	if model.modal == nil {
		t.Fatal("modal = nil, want the pending elicitation")
	}
	if model.modal.kind != ModalKindElicitation || model.modal.elicitation == nil || model.modal.appLink == nil {
		t.Fatalf("modal = %#v", model.modal)
	}
	body := model.modal.body
	for _, want := range []string{"Test Connector", "Install the connector to continue", "Install this app in your browser"} {
		if !strings.Contains(body, want) {
			t.Fatalf("app link popup body missing %q:\n%s", want, body)
		}
	}
	if labels := modalOptionLabels(model.modal.options); labels != "Install on ChatGPT|Back" {
		t.Fatalf("link-screen options = %q", labels)
	}

	// Opening the install URL is a host action; the popup stays on the
	// confirmation screen.
	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyEnter}))
	if len(responses) != 0 {
		t.Fatalf("responses after opening the URL = %#v, want the popup to stay open", responses)
	}
	if len(actions) != 1 ||
		actions[0].Kind != bottompane.AppLinkEventOpenURL ||
		actions[0].URL != "https://example.test/install" {
		t.Fatalf("open-url actions = %#v", actions)
	}
	if model.modal == nil {
		t.Fatal("modal closed after opening the URL, want the confirmation screen")
	}
	if labels := modalOptionLabels(model.modal.options); labels != "I already Installed it|Back" {
		t.Fatalf("confirmation options = %q", labels)
	}

	// Confirming completes the flow and resolves the elicitation.
	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyEnter}))
	if model.modal != nil {
		t.Fatalf("modal = %#v, want the completed popup closed", model.modal)
	}
	if len(responses) != 1 {
		t.Fatalf("responses after confirming = %#v", responses)
	}
	confirmed := responses[0]
	if confirmed.Elicitation == nil || confirmed.Elicitation.Action != "accept" {
		t.Fatalf("confirmation decision = %#v, want accept", confirmed.Elicitation)
	}
	// A non-codex_apps server does not refresh connectors (Rust's
	// complete_external_flow_and_close): the run is exactly the URL handoff and
	// the elicitation resolution.
	kinds := make([]bottompane.AppLinkEventKind, 0, len(actions))
	for _, action := range actions {
		kinds = append(kinds, action.Kind)
	}
	if strings.Join([]string{string(kinds[0]), string(kinds[1])}, "|") != "open_url|resolve_elicitation" {
		t.Fatalf("app link actions = %#v", kinds)
	}
}

func validToolSuggestionRequest(id string) ElicitationRequestMsg {
	return ElicitationRequestMsg{
		ID:         id,
		ServerName: "connector-server",
		Message:    "Install Test Connector",
		RequestedSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Meta: map[string]any{
			"codex_approval_kind": "tool_suggestion",
			"tool_type":           "connector",
			"suggest_type":        "install",
			"suggest_reason":      "Install the connector to continue",
			"tool_id":             "connector_test",
			"tool_name":           "Test Connector",
			"install_url":         "https://example.test/install",
		},
	}
}

func modalOptionLabels(options []ModalOption) string {
	labels := make([]string, 0, len(options))
	for _, option := range options {
		labels = append(labels, option.Label)
	}
	return strings.Join(labels, "|")
}
