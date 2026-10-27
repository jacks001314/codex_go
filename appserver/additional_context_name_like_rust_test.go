package appserver

import (
	"strings"
	"testing"

	"codex_go/turn"
)

func additionalContextNamePointer(value string) *string { return &value }

func additionalContextNameThread(t *testing.T, router *RuntimeRouter) string {
	t.Helper()
	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello names",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	return threadStart.Result.(*ThreadStartResponse).Thread.ID
}

func TestTurnStartAdditionalContextNamesLikeRust(t *testing.T) {
	maximum := strings.Repeat("a", 64)
	for _, test := range []struct {
		name  string
		label *string
	}{
		{name: "legacy caller omits name"},
		{name: "minimum length", label: additionalContextNamePointer("a")},
		{name: "digits and underscore", label: additionalContextNamePointer("browser_2")},
		{name: "maximum length", label: &maximum},
	} {
		t.Run(test.name, func(t *testing.T) {
			router, agent, sink, _ := newAdditionalContextTestRouter(t, "[features]\nuntrusted_as_tool_output = true\n")
			threadID := additionalContextNameThread(t, router)
			start := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
				ThreadID: threadID,
				Prompt:   "inspect tab",
				AdditionalContext: map[string]turn.AdditionalContextEntry{
					"custom_source": {Value: "source value", Kind: turn.AdditionalContextUntrusted, Name: test.label},
				},
			}))
			if start.Error != nil {
				t.Fatalf("turn start error: %+v", start.Error)
			}
			request := waitForRuntimeAgentRequest(t, agent)
			outputs := additionalContextToolOutputs(t, request.InputItems)
			if len(outputs) != 1 {
				t.Fatalf("tool outputs = %#v, want one", outputs)
			}
			wantName := "custom_source"
			if test.label != nil {
				wantName = *test.label
			}
			if got := outputs[0]; got["name"] != wantName || got["namespace"] != "agent" || got["output"] != "source value" {
				t.Fatalf("tool output = %#v, want name %q", got, wantName)
			}
			waitForTurnCompletedStatus(t, sink, start.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)
		})
	}
}

func TestTurnStartRejectsInvalidAdditionalContextNamesBeforeAdmissionLikeRust(t *testing.T) {
	tooLong := strings.Repeat("a", 65)
	for _, test := range []struct {
		name  string
		kind  turn.AdditionalContextKind
		label string
		want  string
	}{
		{name: "empty", kind: turn.AdditionalContextUntrusted, label: "", want: "additional context name must match [a-z][a-z0-9_]{0,63}"},
		{name: "dotted", kind: turn.AdditionalContextUntrusted, label: "agent.browser", want: "additional context name must match [a-z][a-z0-9_]{0,63}"},
		{name: "other dotted", kind: turn.AdditionalContextUntrusted, label: "other.browser", want: "additional context name must match [a-z][a-z0-9_]{0,63}"},
		{name: "leading digit", kind: turn.AdditionalContextUntrusted, label: "1browser", want: "additional context name must match [a-z][a-z0-9_]{0,63}"},
		{name: "uppercase", kind: turn.AdditionalContextUntrusted, label: "Browser", want: "additional context name must match [a-z][a-z0-9_]{0,63}"},
		{name: "non ascii", kind: turn.AdditionalContextUntrusted, label: "é", want: "additional context name must match [a-z][a-z0-9_]{0,63}"},
		{name: "too long", kind: turn.AdditionalContextUntrusted, label: tooLong, want: "additional context name must match [a-z][a-z0-9_]{0,63}"},
		{name: "application", kind: turn.AdditionalContextApplication, label: "browser", want: "additional context name is only supported for untrusted context"},
	} {
		t.Run(test.name, func(t *testing.T) {
			router, agent, sink, _ := newAdditionalContextTestRouter(t, "")
			threadID := additionalContextNameThread(t, router)
			label := test.label
			start := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
				ThreadID: threadID,
				Prompt:   "rejected input",
				AdditionalContext: map[string]turn.AdditionalContextEntry{
					"source": {Value: "rejected context", Kind: test.kind, Name: &label},
				},
			}))
			if start.Error == nil || start.Error.Code != JSONRPCInvalidRequestErrorCode || start.Error.Message != test.want {
				t.Fatalf("turn start error = %+v, want code %d message %q", start.Error, JSONRPCInvalidRequestErrorCode, test.want)
			}
			if sinkHasMethod(sink, NotificationTurnStarted) {
				t.Fatal("rejected context still admitted a turn")
			}

			valid := router.Handle(requestWithParams(t, IntID(3), MethodTurnStart, turn.TurnStartParams{
				ThreadID: threadID,
				Prompt:   "valid input",
			}))
			if valid.Error != nil {
				t.Fatalf("valid turn start error: %+v", valid.Error)
			}
			request := waitForRuntimeAgentRequest(t, agent)
			if text := additionalContextUserMessageText(t, request.InputItems); strings.Contains(text, "rejected") {
				t.Fatalf("rejected input reached the model: %q", text)
			}
			waitForTurnCompletedStatus(t, sink, valid.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)
		})
	}
}

func TestTurnSteerRejectsInvalidAdditionalContextNameLikeRust(t *testing.T) {
	router, _, _, _ := newAdditionalContextTestRouter(t, "")
	label := "agent.browser"
	response := router.Handle(requestWithParams(t, IntID(1), MethodTurnSteer, turn.TurnSteerParams{
		ThreadID:       "thread-steer-names",
		ExpectedTurnID: "turn-does-not-exist",
		Input:          []turn.TurnUserInput{{Type: "text", Text: "rejected input"}},
		AdditionalContext: map[string]turn.AdditionalContextEntry{
			"browser_info": {Value: "rejected context", Kind: turn.AdditionalContextUntrusted, Name: &label},
		},
	}))
	if response.Error == nil || response.Error.Code != JSONRPCInvalidRequestErrorCode ||
		response.Error.Message != "additional context name must match [a-z][a-z0-9_]{0,63}" {
		t.Fatalf("turn steer error = %+v, want invalid-request name error", response.Error)
	}
}
