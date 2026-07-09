package bottompane

import (
	"reflect"
	"testing"

	"codex_go/internal/tui"
)

func TestAppLinkCodexAppsAuthURLRequestMatchesRust(t *testing.T) {
	target := appLinkSuggestionTarget()
	params, ok := AppLinkViewParamsFromURLRequest(target.ThreadID, target.ServerName, target.RequestID, appLinkAuthURLRequest("https://chatgpt.com/apps/google-calendar/connector_calendar"))
	if !ok {
		t.Fatal("expected auth app link params")
	}
	if params.AppID != "connector_calendar" || params.Title != "Google Calendar" || params.URL != "https://chatgpt.com/apps/google-calendar/connector_calendar" {
		t.Fatalf("params = %#v", params)
	}
	if params.SuggestionType == nil || *params.SuggestionType != AppLinkSuggestionAuth {
		t.Fatalf("suggestion type = %#v", params.SuggestionType)
	}
	if params.ElicitationTarget == nil || *params.ElicitationTarget != target {
		t.Fatalf("target = %#v, want %#v", params.ElicitationTarget, target)
	}
}

func TestAppLinkGenericURLRequestMatchesRust(t *testing.T) {
	target := appLinkGenericTarget()
	params, ok := AppLinkViewParamsFromURLRequest(target.ThreadID, target.ServerName, target.RequestID, AppLinkURLRequest{
		Message:       "Review the payment details to continue.",
		URL:           "https://payments.example/checkout/123",
		ElicitationID: "payment-123",
	})
	if !ok {
		t.Fatal("expected generic URL app link params")
	}
	wantDescription := "Server: payments"
	wantReason := "Review the payment details to continue."
	wantSuggestion := AppLinkSuggestionExternalAction
	want := AppLinkViewParams{
		AppID:             "payment-123",
		Title:             "Action required",
		Description:       &wantDescription,
		Instructions:      "Complete the requested action in your browser, then return here.",
		URL:               "https://payments.example/checkout/123",
		IsInstalled:       true,
		IsEnabled:         true,
		SuggestReason:     &wantReason,
		SuggestionType:    &wantSuggestion,
		ElicitationTarget: &target,
	}
	if !reflect.DeepEqual(params, want) {
		t.Fatalf("params = %#v, want %#v", params, want)
	}

	blankReasonParams, ok := AppLinkViewParamsFromURLRequest(target.ThreadID, target.ServerName, target.RequestID, AppLinkURLRequest{
		Message:       "  ",
		URL:           "https://payments.example/checkout/123",
		ElicitationID: "payment-123",
	})
	if !ok || blankReasonParams.SuggestReason == nil || *blankReasonParams.SuggestReason != "  " {
		t.Fatalf("blank reason params = %#v ok=%v", blankReasonParams, ok)
	}
}

func TestAppLinkURLRequestRejectsUntrustedURLs(t *testing.T) {
	target := appLinkSuggestionTarget()
	for _, rawURL := range []string{
		"http://chatgpt.com/apps/google-calendar/connector_calendar",
		"https://user:pass@chatgpt.com/apps/google-calendar/connector_calendar",
		"https://chatgpt.com.evil.example/apps/google-calendar/connector_calendar",
		"https://evilchatgpt.com/apps/google-calendar/connector_calendar",
	} {
		if _, ok := AppLinkViewParamsFromURLRequest(target.ThreadID, target.ServerName, target.RequestID, appLinkAuthURLRequest(rawURL)); ok {
			t.Fatalf("expected auth URL %q to be rejected", rawURL)
		}
	}
	generic := appLinkGenericTarget()
	for _, rawURL := range []string{
		"http://payments.example/checkout/123",
		"https://user:pass@payments.example/checkout/123",
	} {
		if _, ok := AppLinkViewParamsFromURLRequest(generic.ThreadID, generic.ServerName, generic.RequestID, AppLinkURLRequest{
			Message:       "Review the payment details to continue.",
			URL:           rawURL,
			ElicitationID: "payment-123",
		}); ok {
			t.Fatalf("expected generic URL %q to be rejected", rawURL)
		}
	}
}

func TestAppLinkRowsInstallEnableAuthAndGenericMatchRust(t *testing.T) {
	install := NewAppLinkView(appLinkInstallParams())
	rows := install.Rows(72)
	for _, want := range []string{
		"Google Calendar",
		"Plan events and schedules.",
		"Plan and reference events from your calendar",
		"Install this app in your browser, then return here.",
		"Newly installed apps can take a few minutes to appear in /apps.",
		"After installed, use $ to insert this app into the prompt.",
		appLinkDefaultFooterHint,
	} {
		if !bottomPaneContainsRow(rows, want) {
			t.Fatalf("install rows missing %q: %#v", want, rows)
		}
	}
	if !bottomPaneContainsRow(rows, tui.RenderSelectedRow(tui.NumberedSelectionPrefix(0, true)+"Install on ChatGPT")) {
		t.Fatalf("install rows missing selected action: %#v", rows)
	}

	enable := NewAppLinkView(appLinkEnableParams())
	rows = enable.Rows(72)
	for _, want := range []string{
		"Use $ to insert this app into the prompt.",
		"Enable this app to use it for the current request.",
		"Newly installed apps can take a few minutes to appear in /apps.",
		tui.NumberedSelectionPrefix(1, false) + "Enable app",
	} {
		if !bottomPaneContainsRow(rows, want) {
			t.Fatalf("enable rows missing %q: %#v", want, rows)
		}
	}

	authParams, _ := AppLinkViewParamsFromURLRequest(appLinkSuggestionTarget().ThreadID, appLinkSuggestionTarget().ServerName, appLinkSuggestionTarget().RequestID, appLinkAuthURLRequest("https://chatgpt.com/apps/google-calendar/connector_google_calendar"))
	auth := NewAppLinkView(authParams)
	rows = auth.Rows(72)
	for _, want := range []string{
		"Google Calendar",
		"Reconnect Google Calendar on ChatGPT.",
		"URL",
		"https://chatgpt.com/apps/google-calendar/connector_google_calendar",
		"Sign in to this app in your browser, then return here.",
	} {
		if !bottomPaneContainsRow(rows, want) {
			t.Fatalf("auth rows missing %q: %#v", want, rows)
		}
	}
	if !bottomPaneContainsRow(rows, tui.RenderSelectedRow(tui.NumberedSelectionPrefix(0, true)+"Open sign-in URL")) {
		t.Fatalf("auth rows missing selected action: %#v", rows)
	}

	genericParams, _ := AppLinkViewParamsFromURLRequest(appLinkGenericTarget().ThreadID, appLinkGenericTarget().ServerName, appLinkGenericTarget().RequestID, AppLinkURLRequest{
		Message:       "Review the payment details to continue.",
		URL:           "https://payments.example/checkout/123",
		ElicitationID: "payment-123",
	})
	generic := NewAppLinkView(genericParams)
	rows = generic.Rows(72)
	for _, want := range []string{
		"Action required",
		"Server: payments",
		"Review the payment details to continue.",
		"URL",
		"https://payments.example/checkout/123",
		"Complete the requested action in your browser, then return here.",
	} {
		if !bottomPaneContainsRow(rows, want) {
			t.Fatalf("generic rows missing %q: %#v", want, rows)
		}
	}
}

func TestAppLinkGenericURLResolvesWithoutConnectorRefresh(t *testing.T) {
	target := appLinkGenericTarget()
	params, _ := AppLinkViewParamsFromURLRequest(target.ThreadID, target.ServerName, target.RequestID, AppLinkURLRequest{
		Message:       "Review the payment details to continue.",
		URL:           "https://payments.example/checkout/123",
		ElicitationID: "payment-123",
	})
	view := NewAppLinkView(params)
	view.HandleKey("enter")
	if view.Screen != AppLinkScreenInstallConfirmation {
		t.Fatalf("screen = %s", view.Screen)
	}
	view.HandleKey("enter")
	want := []AppLinkEvent{
		{Kind: AppLinkEventOpenURL, URL: "https://payments.example/checkout/123"},
		{Kind: AppLinkEventResolveElicitation, ThreadID: target.ThreadID, ServerName: target.ServerName, RequestID: target.RequestID, Decision: AppLinkElicitationAccept},
	}
	if !reflect.DeepEqual(view.Events(), want) || !view.IsComplete() {
		t.Fatalf("events = %#v complete=%v", view.Events(), view.IsComplete())
	}
}

func TestAppLinkInstallToolSuggestionResolvesAfterConfirmation(t *testing.T) {
	target := appLinkSuggestionTarget()
	view := NewAppLinkView(appLinkInstallParams())
	view.HandleKey("enter")
	if view.Screen != AppLinkScreenInstallConfirmation {
		t.Fatalf("screen = %s", view.Screen)
	}
	view.HandleKey("enter")
	want := []AppLinkEvent{
		{Kind: AppLinkEventOpenURL, URL: "https://example.test/google-calendar"},
		{Kind: AppLinkEventRefreshConnectors, ForceRefetch: true},
		{Kind: AppLinkEventResolveElicitation, ThreadID: target.ThreadID, ServerName: target.ServerName, RequestID: target.RequestID, Decision: AppLinkElicitationAccept},
	}
	if !reflect.DeepEqual(view.Events(), want) || !view.IsComplete() {
		t.Fatalf("events = %#v complete=%v", view.Events(), view.IsComplete())
	}
}

func TestAppLinkDeclineAndEnableToolSuggestionMatchRust(t *testing.T) {
	target := appLinkSuggestionTarget()
	declined := NewAppLinkView(appLinkInstallParams())
	declined.HandleKey("2")
	wantDecline := []AppLinkEvent{{Kind: AppLinkEventResolveElicitation, ThreadID: target.ThreadID, ServerName: target.ServerName, RequestID: target.RequestID, Decision: AppLinkElicitationDecline}}
	if !reflect.DeepEqual(declined.Events(), wantDecline) || !declined.IsComplete() {
		t.Fatalf("decline events = %#v complete=%v", declined.Events(), declined.IsComplete())
	}

	enabled := NewAppLinkView(appLinkEnableParams())
	enabled.HandleKey("2")
	wantEnable := []AppLinkEvent{
		{Kind: AppLinkEventSetAppEnabled, AppID: "connector_google_calendar", Enabled: true},
		{Kind: AppLinkEventResolveElicitation, ThreadID: target.ThreadID, ServerName: target.ServerName, RequestID: target.RequestID, Decision: AppLinkElicitationAccept},
	}
	if !reflect.DeepEqual(enabled.Events(), wantEnable) || !enabled.IsComplete() || !enabled.IsEnabled {
		t.Fatalf("enable events = %#v complete=%v enabled=%v", enabled.Events(), enabled.IsComplete(), enabled.IsEnabled)
	}
}

func TestAppLinkLocalToggleAndSelectionKeysMatchRust(t *testing.T) {
	view := NewAppLinkView(AppLinkViewParams{
		AppID:        "connector_1",
		Title:        "Notion",
		Instructions: "Manage app",
		URL:          "https://example.test/notion",
		IsInstalled:  true,
		IsEnabled:    true,
	})
	view.HandleKey("ctrl-l")
	if view.SelectedAction != 1 {
		t.Fatalf("selected = %d, want 1", view.SelectedAction)
	}
	view.HandleKey("ctrl-h")
	if view.SelectedAction != 0 {
		t.Fatalf("selected = %d, want 0", view.SelectedAction)
	}
	view.HandleKey("2")
	if view.IsComplete() || view.IsEnabled {
		t.Fatalf("local toggle complete=%v enabled=%v", view.IsComplete(), view.IsEnabled)
	}
	want := []AppLinkEvent{{Kind: AppLinkEventSetAppEnabled, AppID: "connector_1", Enabled: false}}
	if !reflect.DeepEqual(view.Events(), want) {
		t.Fatalf("events = %#v", view.Events())
	}
	if !reflect.DeepEqual(view.ActionLabels(), []string{"Manage on ChatGPT", "Enable app", "Back"}) {
		t.Fatalf("labels = %#v", view.ActionLabels())
	}
}

func TestAppLinkDismissAndTerminalTitleActionMatchRust(t *testing.T) {
	view := NewAppLinkView(appLinkEnableParams())
	if !view.TerminalTitleRequiresAction() {
		t.Fatal("tool suggestion should require terminal title action")
	}
	if view.DismissAppServerRequest("other", "request-1") {
		t.Fatal("non-matching request should not dismiss")
	}
	if !view.DismissAppServerRequest("codex_apps", "request-1") || !view.IsComplete() {
		t.Fatalf("dismiss complete=%v", view.IsComplete())
	}

	local := NewAppLinkView(AppLinkViewParams{AppID: "connector_1", Title: "Notion", URL: "https://example.test/notion"})
	if local.TerminalTitleRequiresAction() {
		t.Fatal("regular app link should not require terminal title action")
	}
}

func TestAppLinkConfirmationRowsMatchRust(t *testing.T) {
	target := appLinkGenericTarget()
	params, _ := AppLinkViewParamsFromURLRequest(target.ThreadID, target.ServerName, target.RequestID, AppLinkURLRequest{
		Message:       "Review the payment details to continue.",
		URL:           "https://payments.example/checkout/123",
		ElicitationID: "payment-123",
	})
	view := NewAppLinkView(params)
	view.HandleKey("enter")
	rows := view.Rows(62)
	for _, want := range []string{
		"Finish in Browser",
		"Complete the requested action in the browser window that just",
		"opened.",
		`Then return here and select "I finished".`,
		"Link:",
		"https://payments.example/checkout/123",
	} {
		if !bottomPaneContainsRow(rows, want) {
			t.Fatalf("confirmation rows missing %q: %#v", want, rows)
		}
	}
	if !bottomPaneContainsRow(rows, tui.RenderSelectedRow(tui.NumberedSelectionPrefix(0, true)+"I finished")) {
		t.Fatalf("confirmation rows missing selected action: %#v", rows)
	}
}

func appLinkSuggestionTarget() AppLinkElicitationTarget {
	return AppLinkElicitationTarget{ThreadID: "00000000-0000-0000-0000-000000000001", ServerName: "codex_apps", RequestID: "request-1"}
}

func appLinkGenericTarget() AppLinkElicitationTarget {
	return AppLinkElicitationTarget{ThreadID: "00000000-0000-0000-0000-000000000002", ServerName: "payments", RequestID: "request-2"}
}

func appLinkAuthURLRequest(rawURL string) AppLinkURLRequest {
	return AppLinkURLRequest{
		Meta: map[string]any{
			"_codex_apps": map[string]any{
				"connector_auth_failure": map[string]any{
					"is_auth_failure": true,
					"connector_id":    "connector_calendar",
					"connector_name":  "Google Calendar",
				},
			},
		},
		Message:       "Reconnect Google Calendar on ChatGPT.",
		URL:           rawURL,
		ElicitationID: "codex_apps_auth_call_123",
	}
}

func appLinkInstallParams() AppLinkViewParams {
	description := "Plan events and schedules."
	reason := "Plan and reference events from your calendar"
	suggestion := AppLinkSuggestionInstall
	target := appLinkSuggestionTarget()
	return AppLinkViewParams{
		AppID:             "connector_google_calendar",
		Title:             "Google Calendar",
		Description:       &description,
		Instructions:      "Install this app in your browser, then return here.",
		URL:               "https://example.test/google-calendar",
		IsInstalled:       false,
		IsEnabled:         false,
		SuggestReason:     &reason,
		SuggestionType:    &suggestion,
		ElicitationTarget: &target,
	}
}

func appLinkEnableParams() AppLinkViewParams {
	description := "Plan events and schedules."
	reason := "Plan and reference events from your calendar"
	suggestion := AppLinkSuggestionEnable
	target := appLinkSuggestionTarget()
	return AppLinkViewParams{
		AppID:             "connector_google_calendar",
		Title:             "Google Calendar",
		Description:       &description,
		Instructions:      "Enable this app to use it for the current request.",
		URL:               "https://example.test/google-calendar",
		IsInstalled:       true,
		IsEnabled:         false,
		SuggestReason:     &reason,
		SuggestionType:    &suggestion,
		ElicitationTarget: &target,
	}
}

func bottomPaneContainsRow(rows []string, want string) bool {
	for _, row := range rows {
		if row == want {
			return true
		}
	}
	return false
}
