package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/utils"
)

// Rust #47065: a deprecation notice already retained in the transcript is not
// appended again, distinct notices still appear, and clearing history or
// rebuilding it for another thread lets the notice show up once more.
func TestDeprecationNoticesDeduplicateRetainedHistoryLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	model.State.SetThreadID("thread-a")
	notices := func() int {
		count := 0
		for _, message := range model.State.Messages {
			if message.DeprecationNotice != nil {
				count++
			}
		}
		return count
	}

	// A live delivery followed by the same notice replayed from a hidden thread.
	if ok := model.insertDeprecationNotice("Full-history hydration is deprecated", "Use thread/turns/list."); !ok {
		t.Fatal("the first notice was not inserted")
	}
	model.applyThreadEvent(protocol.DeprecationNotice("Full-history hydration is deprecated", "Use thread/turns/list."))
	if got := notices(); got != 1 {
		t.Fatalf("replayed notice count = %d, want 1", got)
	}
	if !strings.Contains(utils.StripANSI(model.viewString()), "Full-history hydration is deprecated") {
		t.Fatalf("notice missing from the view:\n%s", model.viewString())
	}

	// Different details or a different summary are different notices.
	model.applyThreadEvent(protocol.DeprecationNotice("Full-history hydration is deprecated", "Use thread/items/list."))
	model.applyThreadEvent(protocol.DeprecationNotice("Old flag is deprecated", "Use thread/turns/list."))
	if got := notices(); got != 3 {
		t.Fatalf("distinct notice count = %d, want 3", got)
	}

	// Clearing history resets the deduplication scope.
	model.State.Messages = nil
	if ok := model.insertDeprecationNotice("Full-history hydration is deprecated", "Use thread/turns/list."); !ok {
		t.Fatal("a cleared transcript must show the notice again")
	}

	// Switching threads rebuilds the transcript, which also resets the scope.
	model.resetThreadScopedState()
	model.State.Messages = nil
	model.State.SetThreadID("thread-b")
	if ok := model.insertDeprecationNotice("Full-history hydration is deprecated", "Use thread/turns/list."); !ok {
		t.Fatal("a rebuilt transcript must show the notice again")
	}
	if got := notices(); got != 1 {
		t.Fatalf("notice count after the thread switch = %d, want 1", got)
	}
}
