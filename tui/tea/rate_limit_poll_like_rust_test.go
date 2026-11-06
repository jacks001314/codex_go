package tea

import (
	"testing"
	"time"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

// Rust #42372 app/rate_limit_refresh.rs: the TUI polls account limits on a
// cadence derived from the highest used percentage and feeds the result through
// the same snapshot path as server notifications.
func TestModelRateLimitPollAppliesSnapshotsLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-poll")
	state.HasChatGPTAccount = true
	reads := 0
	model := NewModel(state, Options{
		Width:             100,
		Height:            30,
		HasChatGPTAccount: true,
		OnReadRateLimitSnapshots: func() ([]chatwidget.RateLimitSnapshot, error) {
			reads++
			return []chatwidget.RateLimitSnapshot{{
				LimitID: "codex",
				Primary: &chatwidget.RateLimitWindow{UsedPercent: 99},
			}}, nil
		},
	})
	if model.rateLimitPollInterval() != 60*time.Second {
		t.Fatalf("idle interval = %v, want 60s", model.rateLimitPollInterval())
	}
	_, cmd := model.Update(rateLimitPollTickMsg{})
	if cmd == nil {
		t.Fatal("a poll tick with a reader and ChatGPT account returned no command")
	}
	message := cmd()
	result, ok := message.(rateLimitPollResultMsg)
	if !ok {
		t.Fatalf("poll command produced %T, want rateLimitPollResultMsg", message)
	}
	_, next := model.Update(result)
	if reads != 1 {
		t.Fatalf("reads = %d, want 1", reads)
	}
	snapshot, ok := model.rateLimitSnapshots["codex"]
	if !ok || snapshot.Primary == nil || snapshot.Primary.UsedPercent != 99 {
		t.Fatalf("snapshot not applied: %#v", model.rateLimitSnapshots)
	}
	if model.rateLimitPollInterval() != 5*time.Second {
		t.Fatalf("interval after 99%% = %v, want 5s", model.rateLimitPollInterval())
	}
	if next == nil {
		t.Fatal("the poll chain did not schedule another read")
	}
}

func TestModelRateLimitPollSkipsWithoutAccountOrReaderLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-idle")
	model := NewModel(state, Options{Width: 100, Height: 30})
	if model.rateLimitPollEnabled() {
		t.Fatal("poll enabled without a ChatGPT account or reader")
	}
	_, cmd := model.Update(rateLimitPollTickMsg{})
	if cmd != nil {
		t.Fatalf("idle poll tick returned %v", cmd)
	}
}
