package model

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"codex_go/codexapi"
)

func TestParseResponsesStreamRecoversDeclaredCustomToolFromFunctionCallEnvelope(t *testing.T) {
	javascript := `const result = await tools.shell_command({command: "Write-Output OK"}); text(result.output);`
	response, err := parseResponsesStream(
		context.Background(),
		strings.NewReader(responsesSSE(
			`{"type":"response.created","response":{"id":"resp-1"}}`,
			`{"type":"response.output_item.added","item":{"id":"ctc_1","type":"function_call","call_id":"call-1","name":"exec","arguments":""}}`,
			`{"type":"response.custom_tool_call_input.delta","item_id":"ctc_1","call_id":"call-1","delta":"const result = await tools.shell_command({command: \"Write-Output OK\"}); "}`,
			`{"type":"response.custom_tool_call_input.delta","item_id":"ctc_1","call_id":"call-1","delta":"text(result.output);"}`,
			`{"type":"response.output_item.done","item":{"id":"ctc_1","type":"function_call","call_id":"call-1","name":"exec","arguments":""}}`,
			`{"type":"response.completed","response":{"id":"resp-1","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		)),
		&AgentRequest{
			Prompt: "run command",
			Model:  "gpt-test",
			Tools:  []any{map[string]any{"type": "custom", "name": "exec"}},
		},
		"openai",
		nil,
	)
	if err != nil {
		t.Fatalf("parseResponsesStream() error = %v", err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("items = %#v", response.Items)
	}
	item := response.Items[0]
	if item.Type != "custom_tool_call" || item.Name != "exec" || item.CallID != "call-1" || item.Input != javascript || item.Arguments != "" {
		t.Fatalf("custom tool item = %#v", item)
	}
}

// TestResponseFailedErrorClassifiesPolicyCodesLikeRust mirrors Rust #46306:
// a streaming `bio_policy` failure keeps its own non-retryable classification
// while `invalid_prompt` stays a generic invalid request.
func TestResponseFailedErrorClassifiesPolicyCodesLikeRust(t *testing.T) {
	cases := []struct {
		name        string
		code        string
		message     string
		wantKind    codexapi.APIErrorKind
		wantMessage string
		wantRetry   bool
	}{
		{
			name:        "bio policy preserves message",
			code:        "bio_policy",
			message:     "This request was blocked by bio policy.",
			wantKind:    codexapi.ErrorBioPolicy,
			wantMessage: "This request was blocked by bio policy.",
		},
		{
			name:        "bio policy missing message falls back",
			code:        "bio_policy",
			wantKind:    codexapi.ErrorBioPolicy,
			wantMessage: BioPolicyFallbackMessage,
		},
		{
			name:        "bio policy blank message falls back",
			code:        "bio_policy",
			message:     "   ",
			wantKind:    codexapi.ErrorBioPolicy,
			wantMessage: BioPolicyFallbackMessage,
		},
		{
			name:        "invalid prompt stays an invalid request",
			code:        "invalid_prompt",
			message:     "bad prompt",
			wantKind:    codexapi.ErrorInvalidRequest,
			wantMessage: "bad prompt",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"type":"response.failed","response":{"id":"resp-1","error":{"code":"` + tc.code + `","message":"` + tc.message + `"}}}`
			err := responseFailedError([]byte(raw))
			if err == nil {
				t.Fatal("responseFailedError() = nil, want error")
			}
			var apiErr *codexapi.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error = %#v, want *codexapi.APIError", err)
			}
			if apiErr.Kind != tc.wantKind {
				t.Fatalf("kind = %q, want %q", apiErr.Kind, tc.wantKind)
			}
			if apiErr.Message != tc.wantMessage {
				t.Fatalf("message = %q, want %q", apiErr.Message, tc.wantMessage)
			}
			if got := isRetryableResponsesStreamError(err); got != tc.wantRetry {
				t.Fatalf("isRetryableResponsesStreamError() = %v, want %v", got, tc.wantRetry)
			}
		})
	}
}

func TestResponseFailedErrorParsesMisalignmentDetailsLikeRust(t *testing.T) {
	raw := `{"type":"response.failed","response":{"error":{"code":"misalignment_policy_violation","message":"This request violated the misalignment policy.","misalignment":{"error_type":"unauthorized_data_transfer","detailed_explanation":"Sensitive customer explanation","steer":{"message":"Sensitive customer steering"}}}}}`
	err := responseFailedError([]byte(raw))
	var apiErr *codexapi.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *codexapi.APIError", err)
	}
	if apiErr.Kind != codexapi.ErrorMisalignmentPolicyViolation {
		t.Fatalf("error kind = %q, want misalignmentPolicyViolation", apiErr.Kind)
	}
	if apiErr.Misalignment == nil {
		t.Fatal("misalignment details = nil, want populated")
	}
	if apiErr.Misalignment.ErrorType == nil || *apiErr.Misalignment.ErrorType != "unauthorized_data_transfer" {
		t.Fatalf("error_type = %#v, want unauthorized_data_transfer", apiErr.Misalignment.ErrorType)
	}
	if apiErr.Misalignment.DetailedExplanation == nil || *apiErr.Misalignment.DetailedExplanation != "Sensitive customer explanation" {
		t.Fatalf("detailed_explanation = %#v", apiErr.Misalignment.DetailedExplanation)
	}
	if apiErr.Misalignment.Steer == nil || apiErr.Misalignment.Steer.Message != "Sensitive customer steering" {
		t.Fatalf("steer = %#v", apiErr.Misalignment.Steer)
	}
}

func TestResponseFailedErrorMalformedMisalignmentIsIgnoredLikeRust(t *testing.T) {
	raw := `{"type":"response.failed","response":{"error":{"code":"misalignment_policy_violation","message":"blocked","misalignment":"not-an-object"}}}`
	err := responseFailedError([]byte(raw))
	var apiErr *codexapi.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *codexapi.APIError", err)
	}
	if apiErr.Kind != codexapi.ErrorMisalignmentPolicyViolation {
		t.Fatalf("error kind = %q, want misalignmentPolicyViolation", apiErr.Kind)
	}
	if apiErr.Misalignment != nil {
		t.Fatalf("malformed misalignment should be nil, got %#v", apiErr.Misalignment)
	}
}

func TestResponseFailedErrorMisalignmentAbsentIsNilLikeRust(t *testing.T) {
	raw := `{"type":"response.failed","response":{"error":{"code":"misalignment_policy_violation","message":"This request violated the misalignment policy."}}}`
	err := responseFailedError([]byte(raw))
	var apiErr *codexapi.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *codexapi.APIError", err)
	}
	if apiErr.Misalignment != nil {
		t.Fatalf("misalignment should be nil when absent, got %#v", apiErr.Misalignment)
	}
}

func TestUsageFromStreamEventDataCapturesRolloutBudgetUnits(t *testing.T) {
	usage, ok := usageFromStreamEventData([]byte(`{"response":{"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6,"codex_rollout_budget_units":2.5}}}`))
	if !ok {
		t.Fatal("usageFromStreamEventData() ok = false, want true")
	}
	if usage.InputTokens != 4 || usage.OutputTokens != 2 || usage.TotalTokens != 6 {
		t.Fatalf("usage = %#v", usage)
	}
	if usage.CodexRolloutBudgetUnits != "2.5" {
		t.Fatalf("codex rollout budget units = %q, want 2.5", usage.CodexRolloutBudgetUnits)
	}
}

func TestUsageFromStreamEventDataMissingRolloutBudgetUnits(t *testing.T) {
	usage, ok := usageFromStreamEventData([]byte(`{"response":{"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`))
	if !ok {
		t.Fatal("usageFromStreamEventData() ok = false, want true")
	}
	if usage.CodexRolloutBudgetUnits != "" {
		t.Fatalf("codex rollout budget units = %q, want empty", usage.CodexRolloutBudgetUnits)
	}
}

func TestParseResponsesStreamKeepsDeclaredFunctionToolAsFunctionCall(t *testing.T) {
	response, err := parseResponsesStream(
		context.Background(),
		strings.NewReader(responsesSSE(
			`{"type":"response.created","response":{"id":"resp-1"}}`,
			`{"type":"response.function_call_arguments.delta","item_id":"fc-1","call_id":"call-1","delta":"{\"cmd\":\"pwd\"}"}`,
			`{"type":"response.output_item.done","item":{"id":"fc-1","type":"function_call","call_id":"call-1","name":"exec_command","arguments":""}}`,
			`{"type":"response.completed","response":{"id":"resp-1","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		)),
		&AgentRequest{
			Prompt: "run command",
			Model:  "gpt-test",
			Tools:  []any{map[string]any{"type": "function", "name": "exec_command"}},
		},
		"openai",
		nil,
	)
	if err != nil {
		t.Fatalf("parseResponsesStream() error = %v", err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("items = %#v", response.Items)
	}
	item := response.Items[0]
	if item.Type != "function_call" || item.Name != "exec_command" || item.Arguments != `{"cmd":"pwd"}` || item.Input != "" {
		t.Fatalf("function tool item = %#v", item)
	}
}

func TestParseResponsesStreamPreservesPlaintextCollaborationMarker(t *testing.T) {
	response, err := parseResponsesStream(
		context.Background(),
		strings.NewReader(responsesSSE(
			`{"type":"response.created","response":{"id":"resp-1"}}`,
			`{"type":"response.output_item.added","item":{"id":"fc-1","type":"function_call","namespace":"collaboration","name":"spawn_agent","call_id":"call-1","arguments":"","encrypted_function_args":[]}}`,
			`{"type":"response.function_call_arguments.delta","item_id":"fc-1","call_id":"call-1","delta":"{\"task_name\":\"worker\",\"message\":\"hello\"}"}`,
			`{"type":"response.output_item.done","item":{"id":"fc-1","type":"function_call","namespace":"collaboration","name":"spawn_agent","call_id":"call-1","arguments":""}}`,
			`{"type":"response.completed","response":{"id":"resp-1","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		)),
		&AgentRequest{Model: "gpt-test"},
		"openai",
		nil,
	)
	if err != nil {
		t.Fatalf("parseResponsesStream() error = %v", err)
	}
	if len(response.Items) != 1 || response.Items[0].EncryptedFunctionArgs == nil || len(*response.Items[0].EncryptedFunctionArgs) != 0 {
		t.Fatalf("function call marker = %#v", response.Items)
	}
}

func TestResponsesStreamAccumulatorAppliesCustomToolInputDeltasOverInitialInput(t *testing.T) {
	acc := &responsesStreamAccumulator{
		customToolInputDeltas: map[string]string{"call-1": "*** Begin Patch\n*** Add File: calculator.py\n+def add(a, b):\n+    return a + b\n*** End Patch"},
	}
	item := &AgentItem{Type: "custom_tool_call", ID: "item-1", CallID: "call-1", Input: ""}
	acc.applyToolInputDeltas(item)
	if item.Input == "" || item.Input != acc.customToolInputDeltas["call-1"] {
		t.Fatalf("custom tool input = %q, want accumulated delta", item.Input)
	}
}

func TestResponsesStreamAccumulatorPrefersAccumulatedFunctionArguments(t *testing.T) {
	acc := &responsesStreamAccumulator{
		functionCallArgDeltas: map[string]string{"call-1": `{"cmd":"echo ok"}`},
	}
	item := &AgentItem{Type: "function_call", ID: "item-1", CallID: "call-1", Arguments: `{"cmd":"partial"}`}
	acc.applyToolInputDeltas(item)
	if item.Arguments != `{"cmd":"echo ok"}` {
		t.Fatalf("function arguments = %q, want accumulated delta", item.Arguments)
	}
}

func TestResponsesStreamAccumulatorBridgesApplyPatchCustomDeltaToFunctionCall(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: a.txt\n+hello\n*** End Patch"
	acc := &responsesStreamAccumulator{customToolInputDeltas: map[string]string{"call-1": patch}}
	item := &AgentItem{Type: "function_call", Name: "apply_patch", CallID: "call-1"}
	acc.applyToolInputDeltas(item)
	if item.Arguments != patch {
		t.Fatalf("apply_patch arguments = %q, want custom input delta", item.Arguments)
	}
}

func TestResponsesStreamAccumulatorBridgesApplyPatchFunctionDeltaToCustomCall(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: a.txt\n+hello\n*** End Patch"
	acc := &responsesStreamAccumulator{functionCallArgDeltas: map[string]string{"call-1": patch}}
	item := &AgentItem{Type: "custom_tool_call", Name: "apply_patch", CallID: "call-1"}
	acc.applyToolInputDeltas(item)
	if item.Input != patch {
		t.Fatalf("apply_patch input = %q, want function arguments delta", item.Input)
	}
}

func TestResponsesStreamAccumulatorBridgesApplyPatchMismatchedDeltaKey(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: a.txt\n+hello\n*** End Patch"
	acc := &responsesStreamAccumulator{customToolInputDeltas: map[string]string{"stream-item-1": patch}}
	item := &AgentItem{Type: "function_call", Name: "apply_patch", CallID: "final-call-1"}
	acc.applyToolInputDeltas(item)
	if item.Arguments != patch {
		t.Fatalf("apply_patch arguments = %q, want sole custom delta", item.Arguments)
	}
}

func TestSoleAccumulatedToolInputDeltaRejectsAmbiguousInputs(t *testing.T) {
	if got := soleAccumulatedToolInputDelta(map[string]string{"a": "patch-a", "b": "patch-b"}); got != "" {
		t.Fatalf("sole delta = %q, want empty for ambiguous inputs", got)
	}
}

func stringPtrSafetyBuffering(value string) *string { return &value }

// Mirrors Rust's `treatment_from_headers` (codex-api/src/safety_buffering.rs):
// only the two safety-buffering headers create a treatment, and the faster model
// comes from its own header.
func TestSafetyBufferingTreatmentFromHeadersMatchesRust(t *testing.T) {
	cases := []struct {
		name        string
		headers     map[string]string
		wantPresent bool
		wantModel   *string
	}{
		{name: "no headers"},
		{name: "unrelated header", headers: map[string]string{"x-other": "1"}},
		{name: "enabled only", headers: map[string]string{xCodexSafetyBufferingEnabledHeader: "true"}, wantPresent: true},
		{
			name:        "faster model only",
			headers:     map[string]string{xCodexSafetyBufferingFasterModelHeader: "gpt-fast-header"},
			wantPresent: true,
			wantModel:   stringPtrSafetyBuffering("gpt-fast-header"),
		},
		{
			name: "both headers",
			headers: map[string]string{
				xCodexSafetyBufferingEnabledHeader:     "false",
				xCodexSafetyBufferingFasterModelHeader: "gpt-fast-header",
			},
			wantPresent: true,
			wantModel:   stringPtrSafetyBuffering("gpt-fast-header"),
		},
		{
			name:        "blank model value",
			headers:     map[string]string{xCodexSafetyBufferingFasterModelHeader: ""},
			wantPresent: true,
			wantModel:   stringPtrSafetyBuffering(""),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			headers := http.Header{}
			for name, value := range testCase.headers {
				headers.Set(name, value)
			}
			treatment, present := safetyBufferingTreatmentFromHeaders(headers)
			if present != testCase.wantPresent {
				t.Fatalf("present = %v, want %v", present, testCase.wantPresent)
			}
			got := treatment.fasterModel
			if (got == nil) != (testCase.wantModel == nil) || (got != nil && *got != *testCase.wantModel) {
				t.Fatalf("faster model = %v, want %v", got, testCase.wantModel)
			}
		})
	}
	if _, present := safetyBufferingTreatmentFromHeaders(nil); present {
		t.Fatal("nil headers must not produce a treatment")
	}
}

// Mirrors Rust's `json_headers_to_http_headers`: string, number and boolean
// values convert, while invalid names and unsupported or invalid values are
// dropped.
func TestJSONHeadersToHTTPHeadersMatchesRust(t *testing.T) {
	if mapped := jsonHeadersToHTTPHeaders(nil); mapped != nil {
		t.Fatalf("nil headers = %#v, want nil", mapped)
	}
	if mapped := jsonHeadersToHTTPHeaders(map[string]any{}); mapped != nil {
		t.Fatalf("empty headers = %#v, want nil", mapped)
	}
	mapped := jsonHeadersToHTTPHeaders(map[string]any{
		"x-model":        "gpt-fast",
		"x-count":        float64(3),
		"x-enabled":      true,
		"x-disabled":     false,
		"bad name":       "dropped",
		"x-object":       map[string]any{"a": 1},
		"x-array":        []any{"a"},
		"x-null":         nil,
		"x-bad-value":    "line\nbreak",
		"x-padded-value": " padded ",
	})
	if got := mapped.Get("x-model"); got != "gpt-fast" {
		t.Fatalf("x-model = %q", got)
	}
	if got := mapped.Get("x-count"); got != "3" {
		t.Fatalf("x-count = %q", got)
	}
	if got := mapped.Get("x-enabled"); got != "true" {
		t.Fatalf("x-enabled = %q", got)
	}
	if got := mapped.Get("x-disabled"); got != "false" {
		t.Fatalf("x-disabled = %q", got)
	}
	for _, name := range []string{"bad name", "x-object", "x-array", "x-null", "x-bad-value", "x-padded-value"} {
		if _, ok := mapped[http.CanonicalHeaderKey(name)]; ok {
			t.Fatalf("%s survived conversion: %#v", name, mapped)
		}
	}
	// The converted headers feed the treatment lookup, the WebSocket path's form.
	jsonHeaders := map[string]any{"X-Codex-Safety-Buffering-Faster-Model": "gpt-fast-header"}
	treatment, present := safetyBufferingTreatmentFromJSONHeaders(jsonHeaders)
	if !present || treatment.fasterModel == nil || *treatment.fasterModel != "gpt-fast-header" {
		t.Fatalf("treatment from JSON headers = %#v (present=%v)", treatment, present)
	}
}

// Mirrors Rust's
// `safety_buffering_prefers_wire_retry_model_and_only_falls_back_when_omitted`:
// the payload's own wire `retry_model` wins, an explicit null suppresses the
// fallback, and the header treatment supplies the model only when the payload
// omits the key. A delivered payload always asks the UI to show.
func TestSafetyBufferingPrefersWireRetryModelAndFallsBackLikeRust(t *testing.T) {
	treatment := safetyBufferingTreatment{fasterModel: stringPtrSafetyBuffering("gpt-fast-header")}
	cases := []struct {
		name      string
		payload   string
		wantModel *string
	}{
		{
			name:      "omitted falls back to the header",
			payload:   `{"use_cases":["cyber"],"reasons":["user_risk"]}`,
			wantModel: stringPtrSafetyBuffering("gpt-fast-header"),
		},
		{
			name:      "wire model wins",
			payload:   `{"use_cases":["cyber"],"reasons":["user_risk"],"retry_model":"gpt-fast-wire"}`,
			wantModel: stringPtrSafetyBuffering("gpt-fast-wire"),
		},
		{
			name:      "explicit null suppresses the fallback",
			payload:   `{"use_cases":["cyber"],"reasons":["user_risk"],"retry_model":null}`,
			wantModel: nil,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			event := []byte(`{"type":"response.output_text.delta","delta":"hi","safety_buffering":` + testCase.payload + `}`)
			buffering := safetyBufferingFromStreamMetadata(event, treatment)
			if buffering == nil {
				t.Fatal("expected a safety buffering payload")
			}
			if !buffering.ShowBufferingUI {
				t.Fatal("a delivered payload must ask the UI to show")
			}
			if len(buffering.UseCases) != 1 || buffering.UseCases[0] != "cyber" ||
				len(buffering.Reasons) != 1 || buffering.Reasons[0] != "user_risk" {
				t.Fatalf("payload = %#v", buffering)
			}
			got := buffering.FasterModel
			if (got == nil) != (testCase.wantModel == nil) || (got != nil && *got != *testCase.wantModel) {
				t.Fatalf("faster model = %v, want %v", got, testCase.wantModel)
			}
		})
	}

	// A non-string wire value makes serde reject the payload outright.
	invalid := []byte(`{"type":"response.output_text.delta","safety_buffering":{"use_cases":["cyber"],"reasons":["user_risk"],"retry_model":5}}`)
	if buffering := safetyBufferingFromStreamMetadata(invalid, treatment); buffering != nil {
		t.Fatalf("invalid wire model = %#v, want none", buffering)
	}
}

// Mirrors Rust's `SafetyBuffering` struct: both wire arrays are required, so a
// payload missing either one (or carrying a non-string element) produces no
// event, while an empty pair is still a valid payload.
func TestSafetyBufferingRequiresWireArraysLikeRust(t *testing.T) {
	for _, payload := range []string{
		`{"reasons":["user_risk"]}`,
		`{"use_cases":["cyber"]}`,
		`{"use_cases":["cyber"],"reasons":"user_risk"}`,
		`{"use_cases":[1],"reasons":["user_risk"]}`,
		`{"use_cases":"cyber","reasons":["user_risk"]}`,
	} {
		event := []byte(`{"type":"response.output_text.delta","safety_buffering":` + payload + `}`)
		if buffering := safetyBufferingFromStreamMetadata(event, safetyBufferingTreatment{}); buffering != nil {
			t.Fatalf("payload %s produced %#v, want none", payload, buffering)
		}
	}
	empty := []byte(`{"type":"response.output_text.delta","safety_buffering":{"use_cases":[],"reasons":[]}}`)
	buffering := safetyBufferingFromStreamMetadata(empty, safetyBufferingTreatment{})
	if buffering == nil || !buffering.ShowBufferingUI || len(buffering.UseCases) != 0 || len(buffering.Reasons) != 0 {
		t.Fatalf("empty arrays payload = %#v", buffering)
	}
}

// Rust marks `show_buffering_ui` as `#[serde(skip)]`, so the payload can neither
// supply nor suppress it.
func TestSafetyBufferingForcesVisibilityLikeRust(t *testing.T) {
	event := []byte(`{"type":"response.output_text.delta","safety_buffering":{"use_cases":["cyber"],"reasons":["user_risk"],"show_buffering_ui":false}}`)
	buffering := safetyBufferingFromStreamMetadata(event, safetyBufferingTreatment{})
	if buffering == nil || !buffering.ShowBufferingUI {
		t.Fatalf("payload-supplied visibility = %#v, want forced true", buffering)
	}
	// The typed-metadata form behaves the same way.
	metadata := []byte(`{"type":"response.metadata","metadata":{"type":"safety_buffering","use_cases":["cyber"],"reasons":["user_risk"]}}`)
	buffering = safetyBufferingFromStreamMetadata(metadata, safetyBufferingTreatment{})
	if buffering == nil || !buffering.ShowBufferingUI {
		t.Fatalf("metadata payload = %#v, want forced true", buffering)
	}
}

func TestSafetyBufferingFallsBackToTypedResponseMetadata(t *testing.T) {
	event := []byte(`{"type":"response.metadata","metadata":{"type":"safety_buffering","use_cases":["cyber"],"reasons":["user_risk"]}}`)
	buffering := safetyBufferingFromStreamMetadata(event, safetyBufferingTreatment{})
	if buffering == nil || len(buffering.UseCases) != 1 || buffering.UseCases[0] != "cyber" || len(buffering.Reasons) != 1 || buffering.Reasons[0] != "user_risk" {
		t.Fatalf("safety buffering = %#v", buffering)
	}
}

func TestSafetyBufferingTopLevelPresenceWinsOverMetadata(t *testing.T) {
	event := []byte(`{"type":"response.metadata","safety_buffering":{"use_cases":["top_level"],"reasons":["top"]},"metadata":{"type":"safety_buffering","use_cases":["nested"],"reasons":["nested"]}}`)
	buffering := safetyBufferingFromStreamMetadata(event, safetyBufferingTreatment{})
	if buffering == nil || len(buffering.UseCases) != 1 || buffering.UseCases[0] != "top_level" {
		t.Fatalf("top-level safety buffering should win: %#v", buffering)
	}
}

func TestSafetyBufferingTopLevelMalformedIsAuthoritative(t *testing.T) {
	// A present top-level `safety_buffering` wins even when it is null or not
	// an object (Rust 9558d830f6).
	for _, topLevel := range []string{"null", "false"} {
		event := []byte(`{"type":"response.metadata","safety_buffering":` + topLevel + `,"metadata":{"type":"safety_buffering","use_cases":["nested"],"reasons":["nested"]}}`)
		if buffering := safetyBufferingFromStreamMetadata(event, safetyBufferingTreatment{}); buffering != nil {
			t.Fatalf("malformed top-level %s should win over metadata: %#v", topLevel, buffering)
		}
	}
}

func TestSafetyBufferingIgnoresUnrelatedMetadata(t *testing.T) {
	event := []byte(`{"type":"response.metadata","metadata":{"type":"other_metadata","use_cases":["cyber"]}}`)
	if buffering := safetyBufferingFromStreamMetadata(event, safetyBufferingTreatment{}); buffering != nil {
		t.Fatalf("unrelated metadata should not produce safety buffering: %#v", buffering)
	}
}

func TestResponseFailedErrorClassifiesRateLimitExceededLikeRust(t *testing.T) {
	raw := `{"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded","message":"Rate limit reached for gpt-5.1 in organization org-AAA on tokens per min (TPM): Limit 30000, Used 22999, Requested 12528. Please try again in 11.054s. Visit https://platform.openai.com/account/rate-limits to learn more."}}}`
	err := responseFailedError([]byte(raw))
	if err == nil {
		t.Fatal("responseFailedError() = nil, want error")
	}
	var apiErr *codexapi.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *codexapi.APIError", err)
	}
	if apiErr.Kind != codexapi.ErrorRateLimitExceeded {
		t.Fatalf("error kind = %q, want rateLimitExceeded", apiErr.Kind)
	}
	delay, ok := codexapi.RetryDelayInfo(err)
	if !ok {
		t.Fatal("retry delay not reported for rate_limit_exceeded")
	}
	if delay.Milliseconds() != 11054 {
		t.Fatalf("retry delay = %v, want 11054ms", delay)
	}
}

// TestResponseFailedErrorDistinguishesCapacityFromSlowDownLikeRust mirrors Rust
// #45602: `slow_down` is a retryable rate limit with message-provided timing,
// `server_is_overloaded` keeps the terminal overload classification, and the
// credit/spend-limit codes terminate as quota exhaustion.
func TestResponseFailedErrorDistinguishesCapacityFromSlowDownLikeRust(t *testing.T) {
	t.Run("slow down is a retryable rate limit", func(t *testing.T) {
		raw := `{"type":"response.failed","response":{"error":{"code":"slow_down","message":"Rate limit reached. Please try again in 3s."}}}`
		err := responseFailedError([]byte(raw))
		var apiErr *codexapi.APIError
		if !errors.As(err, &apiErr) || apiErr.Kind != codexapi.ErrorRateLimitExceeded {
			t.Fatalf("error = %#v, want rateLimitExceeded", err)
		}
		if apiErr.Message != "Rate limit reached. Please try again in 3s." {
			t.Fatalf("message = %q", apiErr.Message)
		}
		if !isRetryableResponsesStreamError(err) {
			t.Fatal("slow_down should be retryable")
		}
		delay, ok := codexapi.RetryDelayInfo(err)
		if !ok || delay != 3*time.Second {
			t.Fatalf("retry delay = %v (ok=%v), want 3s", delay, ok)
		}
	})

	t.Run("server overloaded preserves its message", func(t *testing.T) {
		raw := `{"type":"response.failed","response":{"error":{"code":"server_is_overloaded","message":"Selected model is at capacity."}}}`
		err := responseFailedError([]byte(raw))
		var apiErr *codexapi.APIError
		if !errors.As(err, &apiErr) || apiErr.Kind != codexapi.ErrorServerOverloaded {
			t.Fatalf("error = %#v, want serverOverloaded", err)
		}
		if apiErr.Message != "Selected model is at capacity." {
			t.Fatalf("message = %q", apiErr.Message)
		}
	})

	// Rust #47967: Flex-capacity failures terminate the turn without retries
	// and keep their dedicated classification.
	t.Run("flex unavailable terminates", func(t *testing.T) {
		raw := `{"type":"response.failed","response":{"error":{"code":"flex_unavailable","message":"Flex capacity unavailable."}}}`
		err := responseFailedError([]byte(raw))
		var apiErr *codexapi.APIError
		if !errors.As(err, &apiErr) || apiErr.Kind != codexapi.ErrorFlexUnavailable {
			t.Fatalf("error = %#v, want flexUnavailable", err)
		}
		if apiErr.Status != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429", apiErr.Status)
		}
		if isRetryableResponsesStreamError(err) {
			t.Fatal("flex_unavailable must not be retryable")
		}
	})

	// The same classification applies to a standalone streamed `error` event
	// (Rust process_responses_event's "error" arm).
	t.Run("streamed error event terminates", func(t *testing.T) {
		acc := &responsesStreamAccumulator{}
		_, err := acc.apply(&responsesSSEEvent{
			Event: "error",
			Data:  []byte(`{"type":"error","error":{"code":"flex_unavailable","message":"Flex capacity unavailable."}}`),
		}, nil)
		var apiErr *codexapi.APIError
		if !errors.As(err, &apiErr) || apiErr.Kind != codexapi.ErrorFlexUnavailable {
			t.Fatalf("error = %#v, want flexUnavailable", err)
		}
		// Other error events keep the existing buffered-stream behavior.
		_, err = acc.apply(&responsesSSEEvent{
			Event: "error",
			Data:  []byte(`{"type":"error","error":{"code":"rate_limit_exceeded","message":"slow down"}}`),
		}, nil)
		if err != nil {
			t.Fatalf("non-Flex error event = %v, want nil", err)
		}
	})

	for _, code := range []string{
		"credit_balance_exhausted",
		"organization_spend_limit_exceeded",
		"project_spend_limit_exceeded",
	} {
		t.Run(code+" terminates as quota", func(t *testing.T) {
			raw := `{"type":"response.failed","response":{"error":{"code":"` + code + `","message":"quota exhausted"}}}`
			err := responseFailedError([]byte(raw))
			var apiErr *codexapi.APIError
			if !errors.As(err, &apiErr) || apiErr.Kind != codexapi.ErrorQuotaExceeded {
				t.Fatalf("error = %#v, want quotaExceeded", err)
			}
			if isRetryableResponsesStreamError(err) {
				t.Fatal("quota exhaustion must not be retryable")
			}
		})
	}
}

func TestResponseFailedErrorUnknownCodeStaysRetryableLikeRust(t *testing.T) {
	raw := `{"type":"response.failed","response":{"error":{"code":"unknown_error","message":"Rate limit reached. Please try again in 1s."}}}`
	err := responseFailedError([]byte(raw))
	var apiErr *codexapi.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *codexapi.APIError", err)
	}
	if apiErr.Kind != codexapi.ErrorRetryable {
		t.Fatalf("error kind = %q, want retryable", apiErr.Kind)
	}
	if _, ok := codexapi.RetryDelayInfo(err); ok {
		t.Fatal("retry delay reported for unknown error code, want none (Rust preserves only unclassified codes as Retryable without delay)")
	}
}
