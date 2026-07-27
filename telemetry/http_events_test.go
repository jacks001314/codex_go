package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestAnalyticsEventsClientPostsRustTrackEventsRequest(t *testing.T) {
	requests := make(chan *http.Request, 1)
	bodies := make(chan TrackEventsRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload TrackEventsRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload error = %v", err)
		}
		w.WriteHeader(http.StatusOK)
		requests <- r
		bodies <- payload
	}))
	defer server.Close()

	client := NewAnalyticsEventsClient(AnalyticsEventsClientOptions{
		BaseURL: server.URL + "/",
		AuthHeaders: http.Header{
			"Authorization":       []string{"Bearer chatgpt-token"},
			"ChatGPT-Account-ID":  []string{"account-123"},
			"X-OpenAI-Fedramp":    []string{"true"},
			"X-Custom-Analytics":  []string{"kept"},
			"X-Another-Analytics": []string{"also-kept"},
		},
		HTTPClient: server.Client(),
	})
	defer client.Close()

	event := NewCodexTurnEvent(CodexTurnEventInput{
		ThreadID:           "thread-http",
		SessionID:          "session-http",
		TurnID:             "turn-http",
		AppServerClient:    sampleAppServerClientMetadata(),
		Runtime:            sampleRuntimeMetadata(),
		InitializationMode: "new",
		Model:              stringPtrTelemetry("gpt-5"),
		ModelProvider:      "openai",
		ServiceTier:        "default",
		ApprovalPolicy:     "on-request",
		ApprovalsReviewer:  "user",
		Status:             stringPtrTelemetry("completed"),
	})
	client.TrackCodexTurnEvent(context.Background(), event)

	var request *http.Request
	select {
	case request = <-requests:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for analytics request")
	}
	if request.Method != http.MethodPost || request.URL.Path != "/codex/analytics-events/events" {
		t.Fatalf("request = %s %s", request.Method, request.URL.Path)
	}
	if request.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q", request.Header.Get("Content-Type"))
	}
	if request.Header.Get("Authorization") != "Bearer chatgpt-token" ||
		request.Header.Get("ChatGPT-Account-ID") != "account-123" ||
		request.Header.Get("X-OpenAI-Fedramp") != "true" {
		t.Fatalf("auth headers = %#v", request.Header)
	}

	payload := <-bodies
	if len(payload.Events) != 1 {
		t.Fatalf("events = %#v", payload.Events)
	}
	var got CodexTurnEventRequest
	if err := json.Unmarshal(payload.Events[0], &got); err != nil {
		t.Fatalf("decode analytics event error = %v", err)
	}
	if got.EventType != CodexTurnEventType || got.EventParams.TurnID != "turn-http" {
		t.Fatalf("event = %#v", got)
	}
}

func TestAnalyticsEventsClientPostsSkillInvocationEventLikeRust(t *testing.T) {
	bodies := make(chan TrackEventsRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload TrackEventsRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload error = %v", err)
		}
		w.WriteHeader(http.StatusOK)
		bodies <- payload
	}))
	defer server.Close()

	client := NewAnalyticsEventsClient(AnalyticsEventsClientOptions{BaseURL: server.URL, HTTPClient: server.Client()})
	defer client.Close()
	client.TrackSkillInvocationEvent(context.Background(), SkillInvocationEventRequest{
		EventType: SkillInvocationEventType,
		SkillID:   "skill-sha1",
		SkillName: "doc",
		EventParams: SkillInvocationEventParams{
			ProductClientID: stringPtrTelemetry("codex-cli"),
			SkillScope:      stringPtrTelemetry("user"),
			PluginID:        stringPtrTelemetry("sample@openai-curated-remote"),
			RemotePluginID:  stringPtrTelemetry("plugins~Plugin_sample"),
			ThreadID:        stringPtrTelemetry("thread-1"),
			TurnID:          stringPtrTelemetry("turn-1"),
			InvokeType:      stringPtrTelemetry(SkillInvocationTypeExplicit),
			ModelSlug:       stringPtrTelemetry("gpt-5"),
		},
	})

	select {
	case payload := <-bodies:
		if len(payload.Events) != 1 {
			t.Fatalf("events = %#v", payload.Events)
		}
		var event SkillInvocationEventRequest
		if err := json.Unmarshal(payload.Events[0], &event); err != nil {
			t.Fatalf("decode event error = %v", err)
		}
		if event.EventType != SkillInvocationEventType || event.SkillID != "skill-sha1" || event.SkillName != "doc" || event.EventParams.PluginID == nil || *event.EventParams.PluginID != "sample@openai-curated-remote" || event.EventParams.RemotePluginID == nil || *event.EventParams.RemotePluginID != "plugins~Plugin_sample" || event.EventParams.RepoURL != nil {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for skill invocation event")
	}
}

func TestAnalyticsEventsClientPostsThreadInitializedUnionEventLikeRust(t *testing.T) {
	bodies := make(chan TrackEventsRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload TrackEventsRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload error = %v", err)
		}
		w.WriteHeader(http.StatusOK)
		bodies <- payload
	}))
	defer server.Close()

	client := NewAnalyticsEventsClient(AnalyticsEventsClientOptions{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	defer client.Close()

	event := NewCodexThreadInitializedEvent(CodexThreadInitializedEventInput{
		ThreadID:           "thread-http",
		SessionID:          "session-http",
		AppServerClient:    sampleAppServerClientMetadata(),
		Runtime:            sampleRuntimeMetadata(),
		Model:              "gpt-5",
		ThreadSource:       stringPtrTelemetry("user"),
		InitializationMode: "new",
		CreatedAt:          123,
	})
	client.TrackCodexThreadInitializedEvent(context.Background(), event)

	var payload TrackEventsRequest
	select {
	case payload = <-bodies:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for analytics request")
	}
	if len(payload.Events) != 1 {
		t.Fatalf("events = %#v", payload.Events)
	}
	var got CodexThreadInitializedEventRequest
	if err := json.Unmarshal(payload.Events[0], &got); err != nil {
		t.Fatalf("decode analytics event error = %v", err)
	}
	if got.EventType != CodexThreadInitializedEventType || got.EventParams.ThreadID != "thread-http" || got.EventParams.Model != "gpt-5" {
		t.Fatalf("event = %#v", got)
	}
}

func TestAnalyticsEventsClientPostsTurnSteerUnionEventLikeRust(t *testing.T) {
	bodies := make(chan TrackEventsRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload TrackEventsRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload error = %v", err)
		}
		w.WriteHeader(http.StatusOK)
		bodies <- payload
	}))
	defer server.Close()

	client := NewAnalyticsEventsClient(AnalyticsEventsClientOptions{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	defer client.Close()

	event := NewCodexTurnSteerEvent(CodexTurnSteerEventInput{
		ThreadID:        "thread-http",
		SessionID:       "session-http",
		ExpectedTurnID:  stringPtrTelemetry("turn-http"),
		AcceptedTurnID:  stringPtrTelemetry("turn-http"),
		AppServerClient: sampleAppServerClientMetadata(),
		Runtime:         sampleRuntimeMetadata(),
		ThreadSource:    stringPtrTelemetry("user"),
		NumInputImages:  1,
		Result:          TurnSteerResultAccepted,
		CreatedAt:       123,
	})
	client.TrackCodexTurnSteerEvent(context.Background(), event)

	var payload TrackEventsRequest
	select {
	case payload = <-bodies:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for analytics request")
	}
	if len(payload.Events) != 1 {
		t.Fatalf("events = %#v", payload.Events)
	}
	var got CodexTurnSteerEventRequest
	if err := json.Unmarshal(payload.Events[0], &got); err != nil {
		t.Fatalf("decode analytics event error = %v", err)
	}
	if got.EventType != CodexTurnSteerEventType || got.EventParams.ThreadID != "thread-http" || got.EventParams.Result != TurnSteerResultAccepted {
		t.Fatalf("event = %#v", got)
	}
}

func TestAnalyticsEventsClientPostsCompactionUnionEventLikeRust(t *testing.T) {
	bodies := make(chan TrackEventsRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload TrackEventsRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload error = %v", err)
		}
		w.WriteHeader(http.StatusOK)
		bodies <- payload
	}))
	defer server.Close()

	client := NewAnalyticsEventsClient(AnalyticsEventsClientOptions{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	defer client.Close()

	event := NewCodexCompactionEvent(CodexCompactionEventInput{
		ThreadID:                  "thread-http",
		SessionID:                 "session-http",
		TurnID:                    "turn-compact",
		AppServerClient:           sampleAppServerClientMetadata(),
		Runtime:                   sampleRuntimeMetadata(),
		ThreadSource:              stringPtrTelemetry("user"),
		Trigger:                   CompactionTriggerManual,
		Reason:                    CompactionReasonUserRequested,
		Implementation:            CompactionImplementationResponses,
		Phase:                     CompactionPhaseStandaloneTurn,
		Strategy:                  CompactionStrategyMemento,
		Status:                    CompactionStatusCompleted,
		ActiveContextTokensBefore: 131000,
		ActiveContextTokensAfter:  64000,
		StartedAt:                 100,
		CompletedAt:               101,
		DurationMS:                uint64PtrTelemetry(1200),
	})
	client.TrackCodexCompactionEvent(context.Background(), event)

	var payload TrackEventsRequest
	select {
	case payload = <-bodies:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for analytics request")
	}
	if len(payload.Events) != 1 {
		t.Fatalf("events = %#v", payload.Events)
	}
	var got CodexCompactionEventRequest
	if err := json.Unmarshal(payload.Events[0], &got); err != nil {
		t.Fatalf("decode analytics event error = %v", err)
	}
	if got.EventType != CodexCompactionEventType || got.EventParams.ThreadID != "thread-http" || got.EventParams.Status != CompactionStatusCompleted {
		t.Fatalf("event = %#v", got)
	}
}

func TestAnalyticsEventsClientPostsGoalUnionEventLikeRust(t *testing.T) {
	bodies := make(chan TrackEventsRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload TrackEventsRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload error = %v", err)
		}
		w.WriteHeader(http.StatusOK)
		bodies <- payload
	}))
	defer server.Close()

	client := NewAnalyticsEventsClient(AnalyticsEventsClientOptions{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	defer client.Close()

	event := NewCodexGoalEvent(CodexGoalEventInput{
		ThreadID:        "thread-http",
		SessionID:       "session-http",
		AppServerClient: sampleAppServerClientMetadata(),
		Runtime:         sampleRuntimeMetadata(),
		ThreadSource:    stringPtrTelemetry("user"),
		GoalID:          "goal-http",
		EventKind:       GoalEventKindCreated,
		GoalStatus:      "active",
		HasTokenBudget:  true,
	})
	client.TrackCodexGoalEvent(context.Background(), event)

	var payload TrackEventsRequest
	select {
	case payload = <-bodies:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for analytics request")
	}
	if len(payload.Events) != 1 {
		t.Fatalf("events = %#v", payload.Events)
	}
	var got CodexGoalEventRequest
	if err := json.Unmarshal(payload.Events[0], &got); err != nil {
		t.Fatalf("decode analytics event error = %v", err)
	}
	if got.EventType != CodexGoalEventType ||
		got.EventParams.ThreadID != "thread-http" ||
		got.EventParams.GoalID != "goal-http" ||
		got.EventParams.EventKind != GoalEventKindCreated {
		t.Fatalf("event = %#v", got)
	}
}

func TestAnalyticsEventsClientPostsPluginUnionEventLikeRust(t *testing.T) {
	bodies := make(chan TrackEventsRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload TrackEventsRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload error = %v", err)
		}
		w.WriteHeader(http.StatusOK)
		bodies <- payload
	}))
	defer server.Close()

	client := NewAnalyticsEventsClient(AnalyticsEventsClientOptions{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	defer client.Close()

	event := NewCodexPluginEvent(CodexPluginInstalledEventType, CodexPluginMetadata{
		PluginID:        stringPtrTelemetry("sample@test"),
		RemotePluginID:  stringPtrTelemetry("remote-sample"),
		PluginName:      stringPtrTelemetry("sample"),
		MarketplaceName: stringPtrTelemetry("test"),
		HasSkills:       boolPtrTelemetry(true),
		MCPServerCount:  intPtrTelemetry(2),
		ConnectorIDs:    []string{"calendar", "drive"},
		ProductClientID: stringPtrTelemetry("codex_cli_rs"),
	})
	client.TrackCodexPluginInstalledEvent(context.Background(), event)

	var payload TrackEventsRequest
	select {
	case payload = <-bodies:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for analytics request")
	}
	if len(payload.Events) != 1 {
		t.Fatalf("events = %#v", payload.Events)
	}
	var got CodexPluginEventRequest
	if err := json.Unmarshal(payload.Events[0], &got); err != nil {
		t.Fatalf("decode analytics event error = %v", err)
	}
	if got.EventType != CodexPluginInstalledEventType ||
		got.EventParams.PluginID == nil ||
		*got.EventParams.PluginID != "sample@test" ||
		got.EventParams.RemotePluginID == nil ||
		*got.EventParams.RemotePluginID != "remote-sample" ||
		got.EventParams.HasSkills == nil ||
		!*got.EventParams.HasSkills {
		t.Fatalf("event = %#v", got)
	}
}

func TestAnalyticsEventsClientPostsExternalAgentImportUnionEventLikeRust(t *testing.T) {
	bodies := make(chan TrackEventsRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload TrackEventsRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload error = %v", err)
		}
		w.WriteHeader(http.StatusOK)
		bodies <- payload
	}))
	defer server.Close()

	client := NewAnalyticsEventsClient(AnalyticsEventsClientOptions{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	defer client.Close()

	event := NewCodexOnboardingExternalAgentImportCompleteEvent(CodexOnboardingExternalAgentImportCompleteMetadata{
		ImportID:        "import-http",
		Source:          "test_import",
		ItemType:        "SESSIONS",
		SuccessCount:    0,
		FailedCount:     1,
		ProductClientID: stringPtrTelemetry("codex_cli_rs"),
	})
	client.TrackCodexOnboardingExternalAgentImportCompleteEvent(context.Background(), event)

	var payload TrackEventsRequest
	select {
	case payload = <-bodies:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for analytics request")
	}
	if len(payload.Events) != 1 {
		t.Fatalf("events = %#v", payload.Events)
	}
	var got CodexOnboardingExternalAgentImportCompleteEventRequest
	if err := json.Unmarshal(payload.Events[0], &got); err != nil {
		t.Fatalf("decode analytics event error = %v", err)
	}
	if got.EventType != CodexOnboardingExternalAgentImportCompleteEventType ||
		got.EventParams.ImportID != "import-http" ||
		got.EventParams.Source != "test_import" ||
		got.EventParams.ItemType != "SESSIONS" ||
		got.EventParams.FailedCount != 1 {
		t.Fatalf("event = %#v", got)
	}
}

func TestAnalyticsEventsClientPostsHookRunUnionEventLikeRust(t *testing.T) {
	bodies := make(chan TrackEventsRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload TrackEventsRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload error = %v", err)
		}
		w.WriteHeader(http.StatusOK)
		bodies <- payload
	}))
	defer server.Close()

	client := NewAnalyticsEventsClient(AnalyticsEventsClientOptions{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	defer client.Close()

	event := NewCodexHookRunEvent(CodexHookRunMetadataV1{
		ThreadID:        stringPtrTelemetry("thread-http"),
		TurnID:          stringPtrTelemetry("turn-http"),
		ProductClientID: stringPtrTelemetry("codex_cli_rs"),
		ModelSlug:       stringPtrTelemetry("gpt-5"),
		HookName:        stringPtrTelemetry("PreToolUse"),
		HookSource:      stringPtrTelemetry("user"),
		Status:          stringPtrTelemetry("completed"),
	})
	client.TrackCodexHookRunEvent(context.Background(), event)

	var payload TrackEventsRequest
	select {
	case payload = <-bodies:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for analytics request")
	}
	if len(payload.Events) != 1 {
		t.Fatalf("events = %#v", payload.Events)
	}
	var got CodexHookRunEventRequest
	if err := json.Unmarshal(payload.Events[0], &got); err != nil {
		t.Fatalf("decode analytics event error = %v", err)
	}
	if got.EventType != CodexHookRunEventType ||
		got.EventParams.ThreadID == nil ||
		*got.EventParams.ThreadID != "thread-http" ||
		got.EventParams.HookName == nil ||
		*got.EventParams.HookName != "PreToolUse" ||
		got.EventParams.Status == nil ||
		*got.EventParams.Status != "completed" {
		t.Fatalf("event = %#v", got)
	}
}

func TestAnalyticsEventsClientPostsReviewUnionEventLikeRust(t *testing.T) {
	bodies := make(chan TrackEventsRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload TrackEventsRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload error = %v", err)
		}
		w.WriteHeader(http.StatusOK)
		bodies <- payload
	}))
	defer server.Close()

	client := NewAnalyticsEventsClient(AnalyticsEventsClientOptions{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	defer client.Close()

	event := NewCodexReviewEvent(CodexReviewEventParams{
		ThreadID:        "thread-http",
		TurnID:          "turn-http",
		ItemID:          stringPtrTelemetry("item-http"),
		ReviewID:        "user:review-http",
		AppServerClient: sampleAppServerClientMetadata(),
		Runtime:         sampleRuntimeMetadata(),
		ThreadSource:    stringPtrTelemetry("user"),
		SubjectKind:     ReviewSubjectKindCommandExecution,
		SubjectName:     "command_execution",
		Reviewer:        ReviewerUser,
		Trigger:         ReviewTriggerInitial,
		Status:          ReviewStatusApproved,
		Resolution:      ReviewResolutionNone,
		StartedAtMS:     123,
		CompletedAtMS:   125,
		DurationMS:      uint64PtrTelemetry(2),
	})
	client.TrackCodexReviewEvent(context.Background(), event)

	var payload TrackEventsRequest
	select {
	case payload = <-bodies:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for analytics request")
	}
	if len(payload.Events) != 1 {
		t.Fatalf("events = %#v", payload.Events)
	}
	var got CodexReviewEventRequest
	if err := json.Unmarshal(payload.Events[0], &got); err != nil {
		t.Fatalf("decode analytics event error = %v", err)
	}
	if got.EventType != CodexReviewEventType || got.EventParams.ReviewID != "user:review-http" || got.EventParams.Status != ReviewStatusApproved {
		t.Fatalf("event = %#v", got)
	}
}

func TestHTTPAnalyticsExporterIsolatesAcceptedLineFingerprintEventsLikeRust(t *testing.T) {
	bodies := make(chan TrackEventsRequest, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload TrackEventsRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload error = %v", err)
		}
		w.WriteHeader(http.StatusOK)
		bodies <- payload
	}))
	defer server.Close()

	exporter := NewHTTPAnalyticsExporter(AnalyticsEventsClientOptions{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	acceptedOne := AcceptedLineFingerprintEventRequests(&AcceptedLineFingerprintEventInput{
		TurnID:               "turn-accepted-1",
		ThreadID:             "thread-accepted-1",
		CompletedAt:          1,
		AcceptedAddedLines:   1,
		AcceptedDeletedLines: 0,
	})[0]
	acceptedTwo := AcceptedLineFingerprintEventRequests(&AcceptedLineFingerprintEventInput{
		TurnID:               "turn-accepted-2",
		ThreadID:             "thread-accepted-2",
		CompletedAt:          2,
		AcceptedAddedLines:   2,
		AcceptedDeletedLines: 1,
	})[0]
	events := []any{
		NewCodexTurnEvent(CodexTurnEventInput{
			ThreadID:        "thread-1",
			SessionID:       "thread-1",
			TurnID:          "turn-1",
			AppServerClient: sampleAppServerClientMetadata(),
			Runtime:         sampleRuntimeMetadata(),
			ModelProvider:   "openai",
		}),
		NewCodexTurnSteerEvent(CodexTurnSteerEventInput{
			ThreadID:        "thread-2",
			SessionID:       "thread-2",
			AppServerClient: sampleAppServerClientMetadata(),
			Runtime:         sampleRuntimeMetadata(),
		}),
		acceptedOne,
		acceptedTwo,
		NewCodexThreadInitializedEvent(CodexThreadInitializedEventInput{
			ThreadID:        "thread-5",
			SessionID:       "thread-5",
			AppServerClient: sampleAppServerClientMetadata(),
			Runtime:         sampleRuntimeMetadata(),
		}),
		NewCodexTurnEvent(CodexTurnEventInput{
			ThreadID:        "thread-6",
			SessionID:       "thread-6",
			TurnID:          "turn-6",
			AppServerClient: sampleAppServerClientMetadata(),
			Runtime:         sampleRuntimeMetadata(),
			ModelProvider:   "openai",
		}),
	}
	if err := exporter.SendTrackEvents(context.Background(), events); err != nil {
		t.Fatalf("SendTrackEvents error = %v", err)
	}

	gotTypes := [][]string{}
	for i := 0; i < 4; i++ {
		select {
		case payload := <-bodies:
			gotTypes = append(gotTypes, trackEventTypes(t, payload))
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for analytics request %d", i+1)
		}
	}
	wantTypes := [][]string{
		{CodexTurnEventType, CodexTurnSteerEventType},
		{"codex_accepted_line_fingerprints"},
		{"codex_accepted_line_fingerprints"},
		{CodexThreadInitializedEventType, CodexTurnEventType},
	}
	if !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("event batches = %#v, want %#v", gotTypes, wantTypes)
	}
}

func TestAnalyticsEventsClientDisabledWhenConfigFalseLikeRust(t *testing.T) {
	disabled := false
	client := NewAnalyticsEventsClient(AnalyticsEventsClientOptions{
		BaseURL:          "https://chatgpt.example/backend-api",
		AnalyticsEnabled: &disabled,
	})
	if client.Enabled() {
		t.Fatal("client enabled with analytics_enabled=false")
	}
	client.TrackCodexTurnEvent(context.Background(), CodexTurnEventRequest{EventType: CodexTurnEventType})
}

func TestHTTPAnalyticsExporterIgnoresEmptyEventsLikeRust(t *testing.T) {
	requests := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	exporter := NewHTTPAnalyticsExporter(AnalyticsEventsClientOptions{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err := exporter.SendTrackEvents(context.Background(), nil); err != nil {
		t.Fatalf("SendTrackEvents nil error = %v", err)
	}
	select {
	case <-requests:
		t.Fatal("unexpected request for empty events")
	case <-time.After(100 * time.Millisecond):
	}
}

func trackEventTypes(t *testing.T, payload TrackEventsRequest) []string {
	t.Helper()
	types := make([]string, 0, len(payload.Events))
	for _, raw := range payload.Events {
		var event struct {
			EventType string `json:"event_type"`
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatalf("decode event type error = %v", err)
		}
		types = append(types, event.EventType)
	}
	return types
}
