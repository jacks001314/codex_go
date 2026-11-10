package tea

import (
	"strings"
	"testing"
	"time"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
	"codex_go/tui/keymsg"
)

// rateLimitRecoveryTestModel builds a model with a ChatGPT account whose
// account limits are read on demand, matching Rust #60435's
// RateLimitSnapshotSource::AccountUsage path.
func rateLimitRecoveryTestModel(t *testing.T, clock *time.Time, read func() ([]chatwidget.RateLimitSnapshot, error)) *Model {
	t.Helper()
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	state.HasChatGPTAccount = true
	model := NewModel(state, Options{
		Width:             80,
		Height:            24,
		HasChatGPTAccount: true,
		ModelPickerOptions: []codextui.ModelPickerOption{
			{ID: "gpt-5", Label: "GPT 5"},
			{ID: chatwidget.NudgeModelSlug, Label: "Mini", DefaultReasoningEffort: "low"},
		},
		OnReadRateLimitSnapshots: read,
	})
	model.now = func() time.Time { return *clock }
	return model
}

func rateLimitRecoverySnapshot(used float64) chatwidget.RateLimitSnapshot {
	return chatwidget.RateLimitSnapshot{
		LimitID: "codex",
		Primary: &chatwidget.RateLimitWindow{UsedPercent: used},
	}
}

// rateLimitRecoveryShownPrompt opens the lower-cost switch prompt the way a
// high-usage rolling update does.
func rateLimitRecoveryShownPrompt(t *testing.T, model *Model) {
	t.Helper()
	model.Update(RateLimitSnapshotMsg{Snapshot: rateLimitRecoverySnapshot(95)})
	if model.rateLimitSwitchPrompt != chatwidget.RateLimitSwitchPromptShown {
		t.Fatalf("prompt state = %q, want shown", model.rateLimitSwitchPrompt)
	}
	if model.modal == nil || model.modal.kind != ModalKindRateLimitSwitch {
		t.Fatal("switch prompt modal is not open")
	}
}

func rateLimitRecoveryLowRead() ([]chatwidget.RateLimitSnapshot, error) {
	return []chatwidget.RateLimitSnapshot{rateLimitRecoverySnapshot(40)}, nil
}

// TestRateLimitSwitchPromptClearedByFreshAccountRead pins Rust #60435: a fresh
// account usage read whose windows all sit below 90% cancels a pending prompt,
// while a rolling update cannot confirm recovery.
func TestRateLimitSwitchPromptClearedByFreshAccountRead(t *testing.T) {
	clock := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	// An applicable banner holds the prompt pending, which isolates the
	// recovery decision from the modal that would otherwise open.
	model := backendBannerStateModel(t, &BackendBannerView{Title: "Usage limit reached"}, BackendBannerRecoveryInput{})
	model.now = func() time.Time { return clock }
	model.rateLimitSwitchPrompt = chatwidget.RateLimitSwitchPromptPending

	model.applyRateLimitSnapshotFromSource(rateLimitRecoverySnapshot(40), rateLimitSnapshotRollingUpdate, false)
	if model.rateLimitSwitchPrompt != chatwidget.RateLimitSwitchPromptPending {
		t.Fatalf("rolling update changed the prompt state to %q, want pending", model.rateLimitSwitchPrompt)
	}

	model.applyRateLimitSnapshotFromSource(rateLimitRecoverySnapshot(40), rateLimitSnapshotAccountUsage, false)
	if model.rateLimitSwitchPrompt != chatwidget.RateLimitSwitchPromptIdle {
		t.Fatalf("prompt state = %q, want idle after a fresh low read", model.rateLimitSwitchPrompt)
	}
}

// TestRateLimitSwitchPromptKeepsHighUsageOnFreshRead pins that a full account
// read both replaces the cached windows and re-queues the prompt when usage is
// still high.
func TestRateLimitSwitchPromptKeepsHighUsageOnFreshRead(t *testing.T) {
	clock := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	model := rateLimitRecoveryTestModel(t, &clock, rateLimitRecoveryLowRead)
	model.rateLimitSwitchPrompt = chatwidget.RateLimitSwitchPromptPending
	model.hideRateLimitModelNudge = true

	model.applyRateLimitSnapshotFromSource(rateLimitRecoverySnapshot(40), rateLimitSnapshotAccountUsage, false)
	snapshot := model.rateLimitSnapshots["codex"]
	if snapshot.Primary == nil || snapshot.Primary.UsedPercent != 40 {
		t.Fatalf("account read did not replace the cached windows: %#v", snapshot)
	}
}

// TestRateLimitRecoveryIgnoresStaleAccountRead pins Rust #60435's stale-read
// gate: a read that started before the newest rolling update cannot confirm
// recovery, so the visible prompt stays in place.
func TestRateLimitRecoveryIgnoresStaleAccountRead(t *testing.T) {
	clock := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	model := rateLimitRecoveryTestModel(t, &clock, rateLimitRecoveryLowRead)
	rateLimitRecoveryShownPrompt(t, model)

	_, cmd := model.Update(rateLimitPollTickMsg{})
	if cmd == nil {
		t.Fatal("poll tick returned no read command")
	}
	result, ok := cmd().(rateLimitPollResultMsg)
	if !ok {
		t.Fatalf("poll command produced %T, want rateLimitPollResultMsg", cmd())
	}
	// A rolling update lands while the read is in flight.
	model.Update(RateLimitSnapshotMsg{Snapshot: rateLimitRecoverySnapshot(95)})

	model.Update(result)
	if model.rateLimitSwitchPrompt == chatwidget.RateLimitSwitchPromptRecovering {
		t.Fatal("a stale account read replaced the prompt with the recovery notice")
	}
	if model.modal == nil || model.modal.kind != ModalKindRateLimitSwitch {
		t.Fatal("stale read closed the visible switch prompt")
	}
}

// TestRateLimitRecoveryNoticeCountsDownAndDismisses pins Rust #60435's five
// second recovery notice: it replaces the prompt in place, rewrites its
// countdown once a second, and dismisses itself at the deadline.
func TestRateLimitRecoveryNoticeCountsDownAndDismisses(t *testing.T) {
	clock := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	model := rateLimitRecoveryTestModel(t, &clock, rateLimitRecoveryLowRead)
	rateLimitRecoveryShownPrompt(t, model)

	model.applyRateLimitSnapshotFromSource(rateLimitRecoverySnapshot(40), rateLimitSnapshotAccountUsage, false)
	if model.rateLimitSwitchPrompt != chatwidget.RateLimitSwitchPromptRecovering {
		t.Fatalf("prompt state = %q, want recovering", model.rateLimitSwitchPrompt)
	}
	view := model.viewString()
	for _, want := range []string{"no longer close to your usage limit", "Dismissing in 5 seconds", "Dismiss"} {
		if !strings.Contains(view, want) {
			t.Fatalf("recovery notice missing %q:\n%s", want, view)
		}
	}

	clock = clock.Add(2 * time.Second)
	if cmd := model.tickRateLimitRecoveryPrompt(clock); cmd == nil {
		t.Fatal("countdown did not schedule the next refresh")
	}
	if view := model.viewString(); !strings.Contains(view, "Dismissing in 3 seconds") {
		t.Fatalf("countdown did not reach three seconds:\n%s", view)
	}

	clock = clock.Add(2 * time.Second)
	if cmd := model.tickRateLimitRecoveryPrompt(clock); cmd == nil {
		t.Fatal("countdown did not schedule the final refresh")
	}
	if view := model.viewString(); !strings.Contains(view, "Dismissing in 1 second\u2026") {
		t.Fatalf("countdown did not use the singular second:\n%s", view)
	}

	clock = clock.Add(time.Second)
	if cmd := model.tickRateLimitRecoveryPrompt(clock); cmd != nil {
		t.Fatal("expired countdown scheduled another refresh")
	}
	if model.modal != nil && model.modal.kind == ModalKindRateLimitSwitch {
		t.Fatal("expired countdown left the recovery notice open")
	}
	if model.rateLimitSwitchPrompt != chatwidget.RateLimitSwitchPromptShown {
		t.Fatalf("prompt state = %q, want shown after the notice closed", model.rateLimitSwitchPrompt)
	}
}

// TestRateLimitRecoveryNoticeDismissedManually pins that Enter and Esc close
// the recovery notice the way its Dismiss row does, without the cancelled
// notice the switch prompt shows.
func TestRateLimitRecoveryNoticeDismissedManually(t *testing.T) {
	for name, keyType := range map[string]keymsg.KeyType{"enter": keymsg.KeyEnter, "esc": keymsg.KeyEscape} {
		t.Run(name, func(t *testing.T) {
			clock := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
			model := rateLimitRecoveryTestModel(t, &clock, rateLimitRecoveryLowRead)
			rateLimitRecoveryShownPrompt(t, model)
			model.applyRateLimitSnapshotFromSource(rateLimitRecoverySnapshot(40), rateLimitSnapshotAccountUsage, false)
			if model.rateLimitSwitchPrompt != chatwidget.RateLimitSwitchPromptRecovering {
				t.Fatalf("prompt state = %q, want recovering", model.rateLimitSwitchPrompt)
			}

			model.Update(key(keyType))
			if model.modal != nil && model.modal.kind == ModalKindRateLimitSwitch {
				t.Fatal("key did not close the recovery notice")
			}
			if model.rateLimitSwitchPrompt != chatwidget.RateLimitSwitchPromptShown {
				t.Fatalf("prompt state = %q, want shown", model.rateLimitSwitchPrompt)
			}
			if model.notice == "Cancelled" {
				t.Fatal("dismissing the recovery notice reported a cancellation")
			}
		})
	}
}

// TestBackendBannerPreservesRecoveryNotice pins Rust #60435: a banner that
// replaces the switch prompt must leave the recovery state alone instead of
// re-arming it as pending.
func TestBackendBannerPreservesRecoveryNotice(t *testing.T) {
	clock := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	model := rateLimitRecoveryTestModel(t, &clock, rateLimitRecoveryLowRead)
	rateLimitRecoveryShownPrompt(t, model)
	model.applyRateLimitSnapshotFromSource(rateLimitRecoverySnapshot(40), rateLimitSnapshotAccountUsage, false)
	if model.rateLimitSwitchPrompt != chatwidget.RateLimitSwitchPromptRecovering {
		t.Fatalf("prompt state = %q, want recovering", model.rateLimitSwitchPrompt)
	}

	model.applyBackendBannerResult(BackendBannerResultMsg{Read: BackendBannerRead{
		Banner: &BackendBannerView{Title: "Usage limit reached"},
	}})
	if !model.backendBannerApplicable() {
		t.Fatal("banner did not become applicable")
	}
	if model.rateLimitSwitchPrompt != chatwidget.RateLimitSwitchPromptRecovering {
		t.Fatalf("banner changed the recovery state to %q", model.rateLimitSwitchPrompt)
	}
}
