package app

import (
	"context"
	"testing"

	"codex_go/appserver"
	"codex_go/cloudbackend"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// Rust #60462: Cloud question answers resolve to native start-or-steer
// admission; every other submission keeps the generic request path.
func TestCloudQuestionAnswerAdmissionRoutingLikeRust(t *testing.T) {
	cases := []struct {
		mode   ThreadParamsMode
		source codextea.SubmitSource
		want   bool
	}{
		{ThreadParamsModeCloud, codextea.SubmitSourceQuestionAnswer, true},
		{ThreadParamsModeCloud, codextea.SubmitSourcePrompt, false},
		{ThreadParamsModeCloud, codextea.SubmitSourceSafetyRecourse, false},
		{ThreadParamsModeCloud, "", false},
		{ThreadParamsModeEmbedded, codextea.SubmitSourceQuestionAnswer, false},
		{ThreadParamsModeEmbedded, codextea.SubmitSourcePrompt, false},
	}
	for _, test := range cases {
		request := codextea.SubmitRequest{Source: test.source}
		if got := usesCloudQuestionAnswerAdmission(test.mode, request); got != test.want {
			t.Fatalf("mode=%v source=%q => %v, want %v", test.mode, test.source, got, test.want)
		}
	}
}

// Rust #60462: a Cloud question answer reaches the running turn through the
// typed admission with its stable client message id, and a rejected reply
// surfaces an error so the answer returns to the composer.
func TestCloudQuestionAnswerUsesNativeAdmissionLikeRust(t *testing.T) {
	requester := &fakeCloudRequester{notifications: make(chan cloudbackend.Notification, 4)}
	transport := newCloudAppServerTransport(context.Background(), requester)
	defer transport.close()
	client := &remoteAppServerTUIClient{transport: transport, threadParamsMode: ThreadParamsModeCloud}
	state := codextui.NewState(nil)
	state.SetThreadID("thread-cloud")
	request := codextea.SubmitRequest{
		Prompt:   "Europe",
		ClientID: "client-answer",
		Source:   codextea.SubmitSourceQuestionAnswer,
	}

	turnID, err := client.startTurn(context.Background(), nil, state, "thread-cloud", request)
	if err != nil {
		t.Fatalf("startTurn() error = %v", err)
	}
	if turnID != "client-answer" {
		t.Fatalf("turn id = %q, want the admitted turn", turnID)
	}
	if len(requester.submitted) != 1 {
		t.Fatalf("typed admissions = %d, want 1", len(requester.submitted))
	}
	admitted := requester.submitted[0]
	if admitted.ThreadID != "thread-cloud" || admitted.ClientUserMessageID != "client-answer" {
		t.Fatalf("admitted params = %#v", admitted)
	}
	if len(admitted.Input) != 1 || admitted.Input[0].Text != "Europe" {
		t.Fatalf("admitted input = %#v", admitted.Input)
	}
	if admitted.TurnTrigger != "user" {
		t.Fatalf("turn trigger = %q", admitted.TurnTrigger)
	}
	for _, method := range requester.methods {
		if method == appserver.MethodTurnStart {
			t.Fatalf("question answer also used the generic turn/start request: %v", requester.methods)
		}
	}

	requester.submitErr = &cloudbackend.RPCError{Code: -32602, Message: "reply stream closed before submission"}
	if _, err := client.startTurn(context.Background(), nil, state, "thread-cloud", request); err == nil {
		t.Fatal("a rejected Cloud answer must fail so the composer restores it")
	}
}
