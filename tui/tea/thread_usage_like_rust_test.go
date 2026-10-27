package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

func newThreadUsageTestModel(t *testing.T, planType string) *Model {
	t.Helper()
	state := codextui.NewState(&codextui.Options{Model: "gpt-5.4"})
	state.SetThreadID("thread-usage")
	model := NewModel(state, Options{
		Width:             100,
		Height:            30,
		HasChatGPTAccount: true,
		OnReadThreadUsage: func(threadID string) (ThreadUsageReadResult, error) { return ThreadUsageReadResult{}, nil },
	})
	model.chatGPTPlanType = planType
	return model
}

func threadUsageStringPointer(value string) *string { return &value }

func threadUsageInt64Pointer(value int64) *int64 { return &value }

func applyThreadUsageForTest(t *testing.T, model *Model, estimate ThreadUsageEstimate) string {
	t.Helper()
	if cmd := model.applyStatusCommand(); cmd == nil {
		t.Fatal("/status did not request thread usage")
	}
	requestID := model.threadUsagePendingRequestID
	if requestID == 0 {
		t.Fatal("/status did not record a pending thread-usage request")
	}
	model.applyThreadUsageResult(threadUsageResultMsg{
		requestID: requestID,
		result:    ThreadUsageReadResult{Outcome: ThreadUsageAvailable, Usage: estimate},
	})
	return model.State.Messages[0].RawText
}

func TestThreadUsageStatusMatchesRustBreakdown(t *testing.T) {
	model := newThreadUsageTestModel(t, "business")
	raw := applyThreadUsageForTest(t, model, ThreadUsageEstimate{
		ThreadID:                    "thread-usage",
		EstimatedUsageCreditsMicros: 50_000_000,
		EstimatedUsageUSDMicros:     threadUsageInt64Pointer(1_820_000),
		Groups: []ThreadUsageGroup{
			{
				Model:                       threadUsageStringPointer("gpt-5.4"),
				ReasoningEffort:             threadUsageStringPointer("high"),
				Speed:                       threadUsageStringPointer("fast"),
				EstimatedUsageCreditsMicros: 40_000_000,
				InputTokens:                 threadUsageInt64Pointer(100),
				CachedInputTokens:           threadUsageInt64Pointer(20),
				OutputTokens:                threadUsageInt64Pointer(40),
			},
			{
				Model:                       threadUsageStringPointer("gpt-5-mini"),
				ReasoningEffort:             threadUsageStringPointer("medium"),
				Speed:                       threadUsageStringPointer("standard"),
				EstimatedUsageCreditsMicros: 10_000_000,
				InputTokens:                 threadUsageInt64Pointer(20),
				CachedInputTokens:           threadUsageInt64Pointer(5),
				OutputTokens:                threadUsageInt64Pointer(10),
			},
		},
	})
	for _, want := range []string{
		"Thread usage:",
		"50 credits",
		"~$1.82",
		"Models:",
		"GPT-5.4 80%, GPT-5 Mini 20%",
		"Reasoning:",
		"Medium 20%, High 80%",
		"Speed:",
		"Fast mode 80%, Standard 20%",
		"Billed tokens:",
		"120 input (25 cached) + 50 output",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("thread usage status missing %q:\n%s", want, raw)
		}
	}
	for _, want := range []string{
		"  Thread usage:        50 credits \u00b7 ~$1.82",
		"    Models:            GPT-5.4 80%, GPT-5 Mini 20%",
		"    Reasoning:         Medium 20%, High 80%",
		"    Speed:             Fast mode 80%, Standard 20%",
		"    Billed tokens:     120 input (25 cached) + 50 output",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("thread usage status row misaligned; missing %q:\n%s", want, raw)
		}
	}
	if model.threadUsage == nil || model.threadUsage.ThreadID != "thread-usage" {
		t.Fatalf("thread usage estimate = %#v", model.threadUsage)
	}
}

func TestThreadUsageEnt26UsesNativeDollarsOnly(t *testing.T) {
	model := newThreadUsageTestModel(t, "ent26")
	raw := applyThreadUsageForTest(t, model, ThreadUsageEstimate{
		ThreadID:                    "thread-usage",
		EstimatedUsageCreditsMicros: 50_000_000,
		EstimatedUsageUSDMicros:     threadUsageInt64Pointer(1_820_000),
		NativeUsageUSDMicros:        threadUsageInt64Pointer(750_000),
		Groups: []ThreadUsageGroup{{
			Model:                       threadUsageStringPointer("gpt-5.4"),
			EstimatedUsageCreditsMicros: 50_000_000,
			InputTokens:                 threadUsageInt64Pointer(120),
		}},
	})
	if !strings.Contains(raw, "Thread usage:") || !strings.Contains(raw, "~$0.75") {
		t.Fatalf("ENT26 native usage missing:\n%s", raw)
	}
	for _, unwanted := range []string{"50 credits", "~$1.82", "Models:", "Reasoning:", "Speed:", "Billed tokens:"} {
		if strings.Contains(raw, unwanted) {
			t.Fatalf("ENT26 status rendered credit breakdown %q:\n%s", unwanted, raw)
		}
	}
}

func TestThreadUsageEnt26MissingNativeCostHidesRow(t *testing.T) {
	model := newThreadUsageTestModel(t, "ent26")
	raw := applyThreadUsageForTest(t, model, ThreadUsageEstimate{
		ThreadID:                    "thread-usage",
		EstimatedUsageCreditsMicros: 50_000_000,
		EstimatedUsageUSDMicros:     threadUsageInt64Pointer(1_820_000),
	})
	if strings.Contains(raw, "Thread usage:") {
		t.Fatalf("ENT26 status invented a cost without native usage:\n%s", raw)
	}
}

func TestThreadUsageDisabledClearsCachedEstimate(t *testing.T) {
	model := newThreadUsageTestModel(t, "business")
	applyThreadUsageForTest(t, model, ThreadUsageEstimate{
		ThreadID:                    "thread-usage",
		EstimatedUsageCreditsMicros: 50_000_000,
	})
	if cmd := model.applyStatusCommand(); cmd == nil {
		t.Fatal("/status did not request thread usage")
	}
	model.applyThreadUsageResult(threadUsageResultMsg{
		requestID: model.threadUsagePendingRequestID,
		result:    ThreadUsageReadResult{Outcome: ThreadUsageDisabled},
	})
	raw := model.State.Messages[len(model.State.Messages)-1].RawText
	if strings.Contains(raw, "Thread usage:") {
		t.Fatalf("disabled thread usage left the cached estimate visible:\n%s", raw)
	}
	if model.threadUsage != nil || !model.threadUsageDisabled {
		t.Fatalf("disabled state estimate=%#v disabled=%v", model.threadUsage, model.threadUsageDisabled)
	}
}

func TestThreadUsageOverlappingStatusCardsRefreshTogether(t *testing.T) {
	model := newThreadUsageTestModel(t, "business")
	if cmd := model.applyStatusCommand(); cmd == nil {
		t.Fatal("first /status did not request thread usage")
	}
	requestID := model.threadUsagePendingRequestID
	if cmd := model.applyStatusCommand(); cmd != nil {
		t.Fatal("second /status started a duplicate thread-usage request")
	}
	if len(model.threadUsageStatusTargets) != 2 {
		t.Fatalf("thread usage targets = %#v, want both status cards", model.threadUsageStatusTargets)
	}
	model.applyThreadUsageResult(threadUsageResultMsg{
		requestID: requestID,
		result: ThreadUsageReadResult{Outcome: ThreadUsageAvailable, Usage: ThreadUsageEstimate{
			ThreadID:                    "thread-usage",
			EstimatedUsageCreditsMicros: 50_000_000,
		}},
	})
	for index, message := range model.State.Messages {
		if !strings.Contains(message.RawText, "Thread usage:") || !strings.Contains(message.RawText, "50 credits") {
			t.Fatalf("status card %d was not refreshed:\n%s", index, message.RawText)
		}
	}
}

func TestThreadUsageStaleRequestAndThreadAreIgnored(t *testing.T) {
	model := newThreadUsageTestModel(t, "business")
	if cmd := model.applyStatusCommand(); cmd == nil {
		t.Fatal("/status did not request thread usage")
	}
	requestID := model.threadUsagePendingRequestID
	model.applyThreadUsageResult(threadUsageResultMsg{
		requestID: requestID + 1,
		result: ThreadUsageReadResult{Outcome: ThreadUsageAvailable, Usage: ThreadUsageEstimate{
			ThreadID:                    "thread-usage",
			EstimatedUsageCreditsMicros: 50_000_000,
		}},
	})
	if model.threadUsage != nil {
		t.Fatalf("stale request id updated thread usage: %#v", model.threadUsage)
	}
	model.applyThreadUsageResult(threadUsageResultMsg{
		requestID: requestID,
		result: ThreadUsageReadResult{Outcome: ThreadUsageAvailable, Usage: ThreadUsageEstimate{
			ThreadID:                    "another-thread",
			EstimatedUsageCreditsMicros: 50_000_000,
		}},
	})
	if model.threadUsage != nil {
		t.Fatalf("response for another thread updated thread usage: %#v", model.threadUsage)
	}
}

func TestThreadUsageTransientZeroKeepsPreviousEstimate(t *testing.T) {
	model := newThreadUsageTestModel(t, "business")
	applyThreadUsageForTest(t, model, ThreadUsageEstimate{
		ThreadID:                    "thread-usage",
		EstimatedUsageCreditsMicros: 50_000_000,
		EstimatedUsageUSDMicros:     threadUsageInt64Pointer(1_820_000),
		Groups:                      []ThreadUsageGroup{{Model: threadUsageStringPointer("gpt-5.4"), EstimatedUsageCreditsMicros: 50_000_000}},
	})
	if cmd := model.applyStatusCommand(); cmd == nil {
		t.Fatal("/status did not request thread usage")
	}
	model.applyThreadUsageResult(threadUsageResultMsg{
		requestID: model.threadUsagePendingRequestID,
		result: ThreadUsageReadResult{Outcome: ThreadUsageAvailable, Usage: ThreadUsageEstimate{
			ThreadID:                    "thread-usage",
			EstimatedUsageCreditsMicros: 0,
			EstimatedUsageUSDMicros:     threadUsageInt64Pointer(0),
		}},
	})
	if model.threadUsage == nil {
		t.Fatal("transient zero cleared the estimate")
	}
	if model.threadUsage.EstimatedUsageCreditsMicros != 50_000_000 {
		t.Fatalf("credits = %d, want previous 50 credits", model.threadUsage.EstimatedUsageCreditsMicros)
	}
	if model.threadUsage.EstimatedUsageUSDMicros == nil || *model.threadUsage.EstimatedUsageUSDMicros != 1_820_000 {
		t.Fatalf("USD = %#v, want previous ~$1.82", model.threadUsage.EstimatedUsageUSDMicros)
	}
	if len(model.threadUsage.Groups) != 1 {
		t.Fatalf("groups = %#v, want previous breakdown", model.threadUsage.Groups)
	}
}

func TestThreadUsageTemporaryFailuresHaveBoundedRetries(t *testing.T) {
	model := newThreadUsageTestModel(t, "business")
	if cmd := model.applyStatusCommand(); cmd == nil {
		t.Fatal("/status did not request thread usage")
	}
	for attempt := 0; attempt < len(threadUsageRetryDelays); attempt++ {
		requestID := model.threadUsagePendingRequestID
		if requestID == 0 {
			t.Fatalf("attempt %d has no pending request", attempt)
		}
		cmd := model.applyThreadUsageResult(threadUsageResultMsg{
			requestID: requestID,
			err:       errorString("temporarily unavailable"),
		})
		if cmd == nil {
			t.Fatalf("attempt %d did not schedule a retry", attempt)
		}
		if len(model.threadUsageStatusTargets) != 1 {
			t.Fatalf("attempt %d dropped the status target", attempt)
		}
		retryCmd := model.applyThreadUsageRetry(threadUsageRetryMsg{threadID: "thread-usage"})
		if retryCmd == nil || model.threadUsagePendingRequestID == 0 {
			t.Fatalf("attempt %d did not start the retry request", attempt)
		}
	}
	if cmd := model.applyThreadUsageResult(threadUsageResultMsg{
		requestID: model.threadUsagePendingRequestID,
		err:       errorString("still unavailable"),
	}); cmd != nil {
		t.Fatal("thread usage retried after exhausting the bounded attempts")
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

func TestThreadUsagePlanGateAndThreadReset(t *testing.T) {
	free := newThreadUsageTestModel(t, "plus")
	if cmd := free.applyStatusCommand(); cmd != nil {
		t.Fatal("free plan unexpectedly requested thread usage")
	}
	if free.threadUsagePendingRequestID != 0 {
		t.Fatalf("free plan pending thread usage = %d", free.threadUsagePendingRequestID)
	}

	model := newThreadUsageTestModel(t, "business")
	applyThreadUsageForTest(t, model, ThreadUsageEstimate{
		ThreadID:                    "thread-usage",
		EstimatedUsageCreditsMicros: 50_000_000,
	})
	model.resetThreadScopedState()
	if model.threadUsage != nil || model.threadUsagePendingRequestID != 0 || len(model.State.StatusExtraFields) != 0 {
		t.Fatalf("thread reset retained usage state: usage=%#v pending=%d fields=%#v", model.threadUsage, model.threadUsagePendingRequestID, model.State.StatusExtraFields)
	}
}
