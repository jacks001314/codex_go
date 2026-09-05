package turn

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStartCreatesActiveTurn(t *testing.T) {
	service := NewTurnService()
	service.SetClock(func() time.Time { return time.Unix(123, 0) })
	response, err := service.Start(&TurnStartParams{ThreadID: "thread-1", Input: []TurnUserInput{{Text: "hello"}}})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if response.Turn.ID != "turn-1" || response.Turn.Prompt != "hello" || response.Turn.StartedAt != 123 {
		t.Fatalf("unexpected turn: %#v", response.Turn)
	}
	steer, err := service.Steer(&TurnSteerParams{ThreadID: "thread-1", ExpectedTurnID: response.Turn.ID, Prompt: "more"})
	if err != nil {
		t.Fatalf("Steer() error = %v", err)
	}
	if steer.TurnID != "turn-1" {
		t.Fatalf("unexpected steer response: %#v", steer)
	}
}

func TestTurnStartResponseMarshalRustShape(t *testing.T) {
	service := NewTurnService()
	service.SetClock(func() time.Time { return time.Unix(123, 0) })
	response, err := service.Start(&TurnStartParams{
		ThreadID: "thread-1",
		Prompt:   "legacy prompt",
		Input:    []TurnUserInput{{Text: "hello"}},
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal TurnStartResponse error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal TurnStartResponse error = %v", err)
	}
	turnPayload := payload["turn"].(map[string]any)
	for _, legacyKey := range []string{"threadId", "prompt", "input"} {
		if _, ok := turnPayload[legacyKey]; ok {
			t.Fatalf("turn response leaked %q: %#v", legacyKey, turnPayload)
		}
	}
	if turnPayload["itemsView"] != "notLoaded" || turnPayload["status"] != TurnStatusInProgress {
		t.Fatalf("turn response = %#v", turnPayload)
	}
	if items, ok := turnPayload["items"].([]any); !ok || len(items) != 0 {
		t.Fatalf("turn items = %#v", turnPayload["items"])
	}
	for _, key := range []string{"error", "startedAt", "completedAt", "durationMs"} {
		if _, ok := turnPayload[key]; !ok || turnPayload[key] != nil {
			t.Fatalf("%s should be required nullable: %#v", key, turnPayload)
		}
	}
}

func TestTurnStartParamsMarshalIncludesCollaborationMode(t *testing.T) {
	params := TurnStartParams{
		ThreadID: "thread-plan",
		Input:    []TurnUserInput{{Type: "text", Text: "make a plan"}},
		CollaborationMode: map[string]any{
			"mode": "plan",
			"settings": map[string]any{
				"model":                  "gpt-5.4",
				"reasoning_effort":       "medium",
				"developer_instructions": "plan carefully",
			},
		},
	}
	data, err := json.Marshal(&params)
	if err != nil {
		t.Fatalf("Marshal TurnStartParams error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal TurnStartParams error = %v", err)
	}
	mode, ok := payload["collaborationMode"].(map[string]any)
	if !ok || mode["mode"] != "plan" {
		t.Fatalf("collaborationMode = %#v", payload["collaborationMode"])
	}
	settings, ok := mode["settings"].(map[string]any)
	if !ok || settings["reasoning_effort"] != "medium" || settings["developer_instructions"] != "plan carefully" {
		t.Fatalf("collaboration settings = %#v", mode["settings"])
	}
}

func TestInterruptRequiresActiveTurn(t *testing.T) {
	service := NewTurnService()
	if _, err := service.Interrupt(nil); !errors.Is(err, ErrInvalidTurnRequest) {
		t.Fatalf("Interrupt(nil) error = %v, want ErrInvalidTurnRequest", err)
	}
	if _, err := service.Interrupt(&TurnInterruptParams{ThreadID: "thread-1", TurnID: "turn-1"}); err == nil {
		t.Fatalf("expected missing active turn error")
	}
	start, err := service.Start(&TurnStartParams{ThreadID: "thread-1"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := service.Interrupt(&TurnInterruptParams{ThreadID: "thread-1", TurnID: start.Turn.ID}); err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}
	if _, err := service.Steer(&TurnSteerParams{ThreadID: "thread-1", ExpectedTurnID: start.Turn.ID, Prompt: "more"}); err == nil {
		t.Fatalf("expected steer to fail after interrupt")
	}
}

func TestCompleteClearsActiveTurn(t *testing.T) {
	service := NewTurnService()
	start, err := service.Start(&TurnStartParams{ThreadID: "thread-1", Prompt: "hello"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := service.Complete(&TurnCompleteParams{ThreadID: "thread-1", TurnID: start.Turn.ID, Status: TurnStatusCompleted}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if _, err := service.Steer(&TurnSteerParams{ThreadID: "thread-1", ExpectedTurnID: start.Turn.ID, Prompt: "more"}); err == nil {
		t.Fatalf("expected steer to fail after complete")
	}
}

func TestSteerRustStyleErrors(t *testing.T) {
	service := NewTurnService()
	if _, err := service.Start(nil); !errors.Is(err, ErrInvalidTurnRequest) {
		t.Fatalf("Start(nil) error = %v, want ErrInvalidTurnRequest", err)
	}
	if _, err := service.Steer(nil); !errors.Is(err, ErrInvalidTurnRequest) {
		t.Fatalf("Steer(nil) error = %v, want ErrInvalidTurnRequest", err)
	}
	if _, err := service.Steer(&TurnSteerParams{ThreadID: "thread-1", ExpectedTurnID: "turn-1"}); !errors.Is(err, ErrNoActiveTurnToSteer) {
		t.Fatalf("empty steer without active turn error = %v, want ErrNoActiveTurnToSteer", err)
	}
	if _, err := service.Steer(&TurnSteerParams{ThreadID: "thread-1", ExpectedTurnID: "turn-1", Prompt: "more"}); !errors.Is(err, ErrNoActiveTurnToSteer) {
		t.Fatalf("no active turn error = %v", err)
	}
	start, err := service.Start(&TurnStartParams{ThreadID: "thread-1", Prompt: "hello"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := service.Steer(&TurnSteerParams{ThreadID: "thread-1", ExpectedTurnID: start.Turn.ID + "-old", Prompt: "more"}); !errors.Is(err, ErrExpectedTurnMismatch) {
		t.Fatalf("expected turn mismatch error = %v", err)
	}
}

func TestSteerAllowsEmptyInputToResumeActiveTurn(t *testing.T) {
	service := NewTurnService()
	start, err := service.Start(&TurnStartParams{ThreadID: "thread-1", Prompt: "hello"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	// Rust 52d9218424: immediate user-message admission accepts empty input; the
	// turn proceeds with its generated environment context and no user-message item.
	response, err := service.Steer(&TurnSteerParams{ThreadID: "thread-1", ExpectedTurnID: start.Turn.ID})
	if err != nil {
		t.Fatalf("empty steer on active turn error = %v, want success", err)
	}
	if response.TurnID != start.Turn.ID {
		t.Fatalf("empty steer turn id = %q, want %q", response.TurnID, start.Turn.ID)
	}
}

func TestTurnInputRejectsConfigurationUpdate(t *testing.T) {
	params := &TurnStartParams{ThreadID: "thread-1", Input: []TurnUserInput{{Type: "configuration_update"}}}
	if err := params.Validate(); err == nil {
		t.Fatal("TurnStartParams accepted configuration_update input")
	}
	steer := &TurnSteerParams{ThreadID: "thread-1", ExpectedTurnID: "turn-1", Input: []TurnUserInput{{Type: "configuration_update"}}}
	if err := steer.Validate(); err == nil {
		t.Fatal("TurnSteerParams accepted configuration_update input")
	}
}

func TestTurnSteerParamsCarriesApprovalsReviewer(t *testing.T) {
	reviewer := "auto_review"
	params := &TurnSteerParams{ThreadID: "thread-1", ExpectedTurnID: "turn-1", ApprovalsReviewer: &reviewer}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"approvalsReviewer":"auto_review"`) {
		t.Fatalf("Marshal() = %s", data)
	}
}

func TestTurnSteerParamsValidateAllowsApprovalsReviewer(t *testing.T) {
	reviewer := "user"
	if err := (&TurnSteerParams{ThreadID: "thread-1", ExpectedTurnID: "turn-1", ApprovalsReviewer: &reviewer}).Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestTurnSettingsUpdateParamsValidation(t *testing.T) {
	reviewer := "auto_review"
	if err := (&TurnSettingsUpdateParams{ThreadID: "thread-1", TurnID: "turn-1", ApprovalsReviewer: &reviewer}).Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	modelID := "gpt-5"
	if err := (&TurnSettingsUpdateParams{ThreadID: "thread-1", TurnID: "turn-1", Model: &modelID}).Validate(); err != nil {
		t.Fatalf("Validate(model) error = %v", err)
	}
	if err := (&TurnSettingsUpdateParams{ThreadID: "thread-1", TurnID: "turn-1"}).Validate(); err == nil {
		t.Fatal("Validate() accepted an empty update")
	}
	effort := "high"
	summary := "auto"
	tier := "priority"
	if err := (&TurnSettingsUpdateParams{ThreadID: "thread-1", TurnID: "turn-1", Effort: &effort, Summary: &summary, ServiceTier: &tier}).Validate(); err != nil {
		t.Fatalf("Validate(all) error = %v", err)
	}
}

func TestTurnSettingsUpdateParamsSerialization(t *testing.T) {
	reviewer := "user"
	data, err := json.Marshal(&TurnSettingsUpdateParams{ThreadID: "thread-1", TurnID: "turn-1", ApprovalsReviewer: &reviewer})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"approvalsReviewer":"user"`) {
		t.Fatalf("Marshal() = %s", data)
	}
	modelID := "gpt-5"
	data, err = json.Marshal(&TurnSettingsUpdateParams{ThreadID: "thread-1", TurnID: "turn-1", Model: &modelID})
	if err != nil {
		t.Fatalf("Marshal(model) error = %v", err)
	}
	if !strings.Contains(string(data), `"model":"gpt-5"`) {
		t.Fatalf("Marshal(model) = %s", data)
	}
}

func TestTurnStartAcceptsTextAtLimitWithMentionInput(t *testing.T) {
	service := NewTurnService()
	_, err := service.Start(&TurnStartParams{
		ThreadID: "thread-1",
		Input: []TurnUserInput{
			{Type: "text", Text: strings.Repeat("x", MaxUserInputTextChars)},
			{Type: "mention", Name: "README", Path: "README.md"},
		},
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
}

func TestTurnStartRejectsCombinedOversizedTextInput(t *testing.T) {
	service := NewTurnService()
	first := strings.Repeat("x", MaxUserInputTextChars/2)
	second := strings.Repeat("y", MaxUserInputTextChars/2+1)
	_, err := service.Start(&TurnStartParams{
		ThreadID: "thread-1",
		Input: []TurnUserInput{
			{Type: "text", Text: first},
			{Text: second},
			{Type: "localImage", Path: "screenshot.png"},
		},
	})
	if !errors.Is(err, ErrInvalidTurnRequest) {
		t.Fatalf("Start() error = %v, want ErrInvalidTurnRequest", err)
	}
	var tooLarge *InputTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("Start() error = %T, want InputTooLargeError", err)
	}
	if tooLarge.ActualChars != MaxUserInputTextChars+1 {
		t.Fatalf("actual chars = %d, want %d", tooLarge.ActualChars, MaxUserInputTextChars+1)
	}
	want := "Input exceeds the maximum length of 1048576 characters."
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func TestTurnStartRejectsRelativeRuntimeWorkspaceRoots(t *testing.T) {
	service := NewTurnService()
	_, err := service.Start(&TurnStartParams{
		ThreadID:              "thread-1",
		RuntimeWorkspaceRoots: []string{"relative/root"},
	})
	if !errors.Is(err, ErrInvalidTurnRequest) || !strings.Contains(err.Error(), "runtimeWorkspaceRoots must contain absolute paths") {
		t.Fatalf("Start() error = %v, want runtimeWorkspaceRoots invalid turn request", err)
	}

	if _, err := service.Start(&TurnStartParams{
		ThreadID:              "thread-2",
		RuntimeWorkspaceRoots: []string{"/repo", `D:\repo`},
	}); err != nil {
		t.Fatalf("Start() absolute roots error = %v", err)
	}
}

func TestTurnStartRejectsOversizedEnvironmentCWDLikeRust(t *testing.T) {
	service := NewTurnService()
	oversized := strings.Repeat("x", maxTurnEnvironmentCWDBytes+1)
	_, err := service.Start(&TurnStartParams{
		ThreadID: "thread-1",
		Environments: []map[string]any{
			{"environment_id": "env-1", "cwd": "C:\\" + oversized},
		},
	})
	if !errors.Is(err, ErrInvalidTurnRequest) || !strings.Contains(err.Error(), "turn environment working directory exceeds the maximum size") {
		t.Fatalf("Start() error = %v, want oversized-cwd invalid turn request", err)
	}

	// A cwd at the 8 KiB boundary is accepted (Rust #39040: > MAX rejects).
	boundary := "/" + strings.Repeat("x", maxTurnEnvironmentCWDBytes-1)
	if _, err := service.Start(&TurnStartParams{
		ThreadID: "thread-2",
		Environments: []map[string]any{
			{"environment_id": "env-1", "cwd": boundary},
		},
	}); err != nil {
		t.Fatalf("Start() boundary-cwd error = %v", err)
	}

	// Environments without a cwd are unaffected.
	if _, err := service.Start(&TurnStartParams{
		ThreadID: "thread-3",
		Environments: []map[string]any{
			{"environment_id": "env-1"},
		},
	}); err != nil {
		t.Fatalf("Start() cwd-less environment error = %v", err)
	}
}

func TestTurnSteerRejectsOversizedTextInput(t *testing.T) {
	service := NewTurnService()
	start, err := service.Start(&TurnStartParams{ThreadID: "thread-1", Prompt: "hello"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	_, err = service.Steer(&TurnSteerParams{
		ThreadID:       "thread-1",
		ExpectedTurnID: start.Turn.ID,
		Input:          []TurnUserInput{{Text: strings.Repeat("z", MaxUserInputTextChars+1)}},
	})
	if !errors.Is(err, ErrInvalidTurnRequest) {
		t.Fatalf("Steer() error = %v, want ErrInvalidTurnRequest", err)
	}
	var tooLarge *InputTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("Steer() error = %T, want InputTooLargeError", err)
	}
	if tooLarge.ActualChars != MaxUserInputTextChars+1 {
		t.Fatalf("actual chars = %d, want %d", tooLarge.ActualChars, MaxUserInputTextChars+1)
	}
}

func TestTurnParamsMarshalRustStableRequestShape(t *testing.T) {
	mode := "proactive"
	start := TurnStartParams{
		ThreadID: "thread-1",
		Prompt:   "legacy prompt",
		Input:    []TurnUserInput{{Text: "hello"}},
		ResponsesAPIMetadata: map[string]string{
			"workspace_kind": "git",
			"thread_id":      "bad",
		},
		Originator:            "codex_vscode",
		RuntimeWorkspaceRoots: []string{"D:/repo"},
		MultiAgentMode:        &mode,
	}
	data, err := json.Marshal(&start)
	if err != nil {
		t.Fatalf("Marshal start error = %v", err)
	}
	var startPayload map[string]any
	if err := json.Unmarshal(data, &startPayload); err != nil {
		t.Fatalf("Unmarshal start error = %v", err)
	}
	for _, legacyKey := range []string{"prompt", "responsesapiClientMetadata", "originator", "runtimeWorkspaceRoots", "multiAgentMode"} {
		if _, ok := startPayload[legacyKey]; ok {
			t.Fatalf("start payload leaked %q: %#v", legacyKey, startPayload)
		}
	}
	inputs, ok := startPayload["input"].([]any)
	if !ok || len(inputs) != 2 || inputs[0].(map[string]any)["text"] != "legacy prompt" {
		t.Fatalf("start input = %#v", startPayload["input"])
	}

	steer := TurnSteerParams{
		ThreadID:       "thread-1",
		ExpectedTurnID: "turn-1",
		Prompt:         "steer prompt",
		ResponsesAPIMetadata: map[string]string{
			"workspace_kind": "projectless",
			"turn_id":        "bad",
		},
		AdditionalContext: map[string]AdditionalContextEntry{"ctx": {Value: "hidden"}},
	}
	data, err = json.Marshal(&steer)
	if err != nil {
		t.Fatalf("Marshal steer error = %v", err)
	}
	var steerPayload map[string]any
	if err := json.Unmarshal(data, &steerPayload); err != nil {
		t.Fatalf("Unmarshal steer error = %v", err)
	}
	for _, legacyKey := range []string{"prompt", "responsesapiClientMetadata", "additionalContext"} {
		if _, ok := steerPayload[legacyKey]; ok {
			t.Fatalf("steer payload leaked %q: %#v", legacyKey, steerPayload)
		}
	}
	inputs, ok = steerPayload["input"].([]any)
	if !ok || len(inputs) != 1 || inputs[0].(map[string]any)["text"] != "steer prompt" {
		t.Fatalf("steer input = %#v", steerPayload["input"])
	}
}

func TestTurnStartParamsUnmarshalServiceTierPresence(t *testing.T) {
	var omitted TurnStartParams
	if err := json.Unmarshal([]byte(`{"threadId":"thread-1","input":[]}`), &omitted); err != nil {
		t.Fatalf("Unmarshal omitted serviceTier error = %v", err)
	}
	if omitted.ServiceTierSet || omitted.ServiceTier != nil {
		t.Fatalf("omitted serviceTier decoded as set: %#v", omitted)
	}

	var cleared TurnStartParams
	if err := json.Unmarshal([]byte(`{"threadId":"thread-1","serviceTier":null,"input":[]}`), &cleared); err != nil {
		t.Fatalf("Unmarshal null serviceTier error = %v", err)
	}
	if !cleared.ServiceTierSet || cleared.ServiceTier != nil {
		t.Fatalf("null serviceTier decoded incorrectly: %#v", cleared)
	}

	var explicit TurnStartParams
	if err := json.Unmarshal([]byte(`{"threadId":"thread-1","serviceTier":"priority","input":[]}`), &explicit); err != nil {
		t.Fatalf("Unmarshal string serviceTier error = %v", err)
	}
	if !explicit.ServiceTierSet || explicit.ServiceTier == nil || *explicit.ServiceTier != "priority" {
		t.Fatalf("string serviceTier decoded incorrectly: %#v", explicit)
	}
}

func TestTurnStartParamsUnmarshalDeprecatedMultiAgentMode(t *testing.T) {
	var params TurnStartParams
	if err := json.Unmarshal([]byte(`{"threadId":"thread-1","multiAgentMode":"proactive","input":[]}`), &params); err != nil {
		t.Fatalf("Unmarshal multiAgentMode error = %v", err)
	}
	if params.MultiAgentMode == nil || *params.MultiAgentMode != "proactive" {
		t.Fatalf("multiAgentMode decoded incorrectly: %#v", params)
	}
}

func TestTurnStartParamsUnmarshalPersonalityPresence(t *testing.T) {
	var omitted TurnStartParams
	if err := json.Unmarshal([]byte(`{"threadId":"thread-1","input":[]}`), &omitted); err != nil {
		t.Fatalf("Unmarshal omitted personality error = %v", err)
	}
	if omitted.PersonalitySet || omitted.Personality != nil {
		t.Fatalf("omitted personality decoded incorrectly: %#v", omitted)
	}

	var explicit TurnStartParams
	if err := json.Unmarshal([]byte(`{"threadId":"thread-1","personality":"friendly","input":[]}`), &explicit); err != nil {
		t.Fatalf("Unmarshal personality error = %v", err)
	}
	if !explicit.PersonalitySet || explicit.Personality == nil || *explicit.Personality != "friendly" {
		t.Fatalf("personality decoded incorrectly: %#v", explicit)
	}
}

func TestTurnUserInputMarshalRustUnionShapes(t *testing.T) {
	text := TurnUserInput{Text: "hello"}
	data, err := json.Marshal(&text)
	if err != nil {
		t.Fatalf("Marshal text input: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal text input: %v", err)
	}
	if payload["type"] != "text" || payload["text"] != "hello" {
		t.Fatalf("text input = %#v", payload)
	}
	if elements, ok := payload["text_elements"].([]any); !ok || len(elements) != 0 {
		t.Fatalf("text_elements = %#v", payload["text_elements"])
	}

	placeholder := "file.go"
	text = TurnUserInput{
		Type: "text",
		Text: "@file",
		TextElements: []TextElement{{
			ByteRange:   ByteRange{Start: 0, End: 5},
			Placeholder: &placeholder,
		}},
	}
	data, err = json.Marshal(&text)
	if err != nil {
		t.Fatalf("Marshal text element input: %v", err)
	}
	payload = map[string]any{}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal text element input: %v", err)
	}
	element := payload["text_elements"].([]any)[0].(map[string]any)
	if element["placeholder"] != "file.go" {
		t.Fatalf("text element = %#v", element)
	}

	mention := TurnUserInput{Type: "mention", Name: "README", Path: "README.md"}
	data, err = json.Marshal(&mention)
	if err != nil {
		t.Fatalf("Marshal mention input: %v", err)
	}
	payload = map[string]any{}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal mention input: %v", err)
	}
	if payload["type"] != "mention" || payload["name"] != "README" || payload["path"] != "README.md" {
		t.Fatalf("mention input = %#v", payload)
	}
}

func TestTurnStartParamsToolOutputValidationLikeRust(t *testing.T) {
	// Empty toolOutput.name is rejected.
	bad := &TurnStartParams{ThreadID: "thread-1", ToolOutput: &TurnToolOutput{Name: "", Output: "x"}}
	if err := bad.Validate(); !errors.Is(err, ErrInvalidTurnRequest) {
		t.Fatalf("empty toolOutput.name error = %v, want ErrInvalidTurnRequest", err)
	}
	// toolOutput combined with nonempty input is rejected.
	combined := &TurnStartParams{ThreadID: "thread-1", ToolOutput: &TurnToolOutput{Name: "notifications", Output: "x"}, Input: []TurnUserInput{{Text: "hello"}}}
	if err := combined.Validate(); !errors.Is(err, ErrInvalidTurnRequest) {
		t.Fatalf("toolOutput + input error = %v, want ErrInvalidTurnRequest", err)
	}
	// A valid toolOutput passes.
	valid := &TurnStartParams{ThreadID: "thread-1", ToolOutput: &TurnToolOutput{Name: "notifications", Namespace: "slack", Output: "Alice mentioned you."}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid toolOutput error = %v", err)
	}
	// Oversized tool output is rejected with InputTooLargeError.
	tooLarge := &TurnStartParams{ThreadID: "thread-1", ToolOutput: &TurnToolOutput{Name: "notifications", Output: strings.Repeat("x", MaxUserInputTextChars+1)}}
	tooLargeErr := tooLarge.Validate()
	if tooLargeErr == nil {
		t.Fatal("oversized toolOutput error = nil, want InputTooLargeError")
	}
	var inputTooLarge *InputTooLargeError
	if !errors.As(tooLargeErr, &inputTooLarge) {
		t.Fatalf("oversized toolOutput error = %T, want InputTooLargeError", tooLargeErr)
	}
}
