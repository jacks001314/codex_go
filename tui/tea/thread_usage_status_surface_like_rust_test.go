package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

// Rust #38282: selecting thread-credits/estimated-thread-cost on the status
// line or terminal title fetches the estimate once and refreshes both surfaces.
func TestStatusLineThreadUsageItemFetchesAndRefreshesTitleLikeRust(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5.4"})
	state.SetThreadID("thread-usage")
	calls := 0
	var titleWrites []string
	model := NewModel(state, Options{
		Width:             100,
		Height:            30,
		HasChatGPTAccount: true,
		OnReadThreadUsage: func(threadID string) (ThreadUsageReadResult, error) {
			calls++
			return ThreadUsageReadResult{Outcome: ThreadUsageAvailable, Usage: ThreadUsageEstimate{
				ThreadID:                    "thread-usage",
				EstimatedUsageCreditsMicros: 5_200_000,
				EstimatedUsageUSDMicros:     threadUsageInt64Pointer(1_820_000),
			}}, nil
		},
		OnWriteTerminalTitle: func(sequence string) bubbletea.Cmd {
			titleWrites = append(titleWrites, sequence)
			return nil
		},
	})
	model.chatGPTPlanType = "business"
	model.ensureStatusControls()
	model.statusLineConfiguredByUser = true
	model.statusControls.StatusLineConfigured = true
	model.statusControls.StatusLineIDs = []string{"model", "estimated-thread-cost"}
	model.statusControls.TerminalTitleConfigured = true
	model.statusControls.TerminalTitleIDs = []string{"project-name", "estimated-thread-cost"}

	runModelCmd(model, model.refreshStatusControlsCmd())
	if calls != 1 {
		t.Fatalf("thread usage reads = %d, want 1", calls)
	}
	if model.threadUsage == nil {
		t.Fatal("estimate was not stored")
	}
	if got, ok := model.statusControls.StatusLineValueForItem(bottompane.StatusLineEstimatedThreadCost); !ok || got != "~$1.82" {
		t.Fatalf("status line cost = %q ok=%v", got, ok)
	}
	if !strings.Contains(model.statusControls.LastTerminalTitle, "~$1.82") {
		t.Fatalf("terminal title = %q", model.statusControls.LastTerminalTitle)
	}
	if len(titleWrites) == 0 || !strings.Contains(titleWrites[len(titleWrites)-1], "~$1.82") {
		t.Fatalf("terminal title writes = %#v", titleWrites)
	}

	// A second refresh reuses the stored estimate instead of re-fetching.
	runModelCmd(model, model.refreshStatusControlsCmd())
	if calls != 1 {
		t.Fatalf("thread usage reads after refresh = %d, want 1", calls)
	}

	// Dropping the item from both surfaces stops polling.
	model.statusControls.StatusLineIDs = []string{"model"}
	model.statusControls.TerminalTitleIDs = []string{"project-name"}
	runModelCmd(model, model.refreshStatusControlsCmd())
	if model.threadUsageRequested || model.threadUsageSurfaceSelected {
		t.Fatalf("polling state after clearing items: requested=%v selected=%v", model.threadUsageRequested, model.threadUsageSurfaceSelected)
	}
	if model.threadUsage == nil {
		t.Fatal("clearing the item must keep the cached estimate")
	}
}

// Rust #57251: ENT26 hides the credit item while the dollar item keeps using
// native usage, and a missing native amount is omitted rather than shown as $0.
func TestStatusLineThreadUsageEnt26LikeRust(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5.4"})
	state.SetThreadID("thread-usage")
	model := NewModel(state, Options{
		Width:             100,
		Height:            30,
		HasChatGPTAccount: true,
		OnReadThreadUsage: func(threadID string) (ThreadUsageReadResult, error) {
			return ThreadUsageReadResult{Outcome: ThreadUsageAvailable, Usage: ThreadUsageEstimate{
				ThreadID:                    "thread-usage",
				EstimatedUsageCreditsMicros: 50_000_000,
				EstimatedUsageUSDMicros:     threadUsageInt64Pointer(1_820_000),
			}}, nil
		},
	})
	model.chatGPTPlanType = "ent26"
	model.ensureStatusControls()
	model.statusLineConfiguredByUser = true
	model.statusControls.StatusLineConfigured = true
	model.statusControls.StatusLineIDs = []string{"thread-credits", "estimated-thread-cost"}

	runModelCmd(model, model.refreshStatusControlsCmd())
	if _, ok := model.statusControls.StatusLineValueForItem(bottompane.StatusLineThreadCredits); ok {
		t.Fatal("ENT26 must not render credits")
	}
	if _, ok := model.statusControls.StatusLineValueForItem(bottompane.StatusLineEstimatedThreadCost); ok {
		t.Fatal("ENT26 without native usage must not render a dollar estimate")
	}
}

// Rust #38282: a completed turn re-reads the estimate so the status surfaces do
// not keep a stale cost, and settlement ends once the amounts grow past the
// pre-turn baseline.
func TestThreadUsageSettlementAfterTurnLikeRust(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5.4"})
	state.SetThreadID("thread-usage")
	credits := int64(50_000_000)
	usd := int64(1_820_000)
	model := NewModel(state, Options{
		Width:             100,
		Height:            30,
		HasChatGPTAccount: true,
		OnReadThreadUsage: func(threadID string) (ThreadUsageReadResult, error) {
			estimatedCredits := credits
			estimatedUSD := usd
			return ThreadUsageReadResult{Outcome: ThreadUsageAvailable, Usage: ThreadUsageEstimate{
				ThreadID:                    "thread-usage",
				EstimatedUsageCreditsMicros: estimatedCredits,
				EstimatedUsageUSDMicros:     &estimatedUSD,
			}}, nil
		},
	})
	model.chatGPTPlanType = "business"
	model.ensureStatusControls()
	model.statusControls.StatusLineConfigured = true
	model.statusControls.StatusLineIDs = []string{"estimated-thread-cost"}

	runModelCmd(model, model.refreshStatusControlsCmd())
	if got, ok := model.statusControls.StatusLineValueForItem(bottompane.StatusLineEstimatedThreadCost); !ok || got != "~$1.82" {
		t.Fatalf("initial cost = %q ok=%v", got, ok)
	}
	if model.threadUsageSettlementActive {
		t.Fatal("settlement must not run before a turn finishes")
	}

	credits = 60_000_000
	usd = 2_100_000
	if cmd := model.refreshThreadUsageAfterTurn(); cmd == nil {
		t.Fatal("turn completion did not refresh the estimate")
	}
	if !model.threadUsageSettlementActive {
		t.Fatal("turn completion must start settlement polling")
	}
	requestID := model.threadUsagePendingRequestID
	if requestID == 0 {
		t.Fatal("turn completion did not request the estimate immediately")
	}
	runModelCmd(model, model.applyThreadUsageResult(threadUsageResultMsg{
		requestID: requestID,
		result: ThreadUsageReadResult{Outcome: ThreadUsageAvailable, Usage: ThreadUsageEstimate{
			ThreadID:                    "thread-usage",
			EstimatedUsageCreditsMicros: credits,
			EstimatedUsageUSDMicros:     threadUsageInt64Pointer(usd),
		}},
	}))
	if model.threadUsageSettlementActive {
		t.Fatal("settlement must end once the amounts grew past the baseline")
	}
	if got, ok := model.statusControls.StatusLineValueForItem(bottompane.StatusLineEstimatedThreadCost); !ok || got != "~$2.10" {
		t.Fatalf("settled cost = %q ok=%v", got, ok)
	}
}
