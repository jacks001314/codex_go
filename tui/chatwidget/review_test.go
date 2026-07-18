package chatwidget

import "testing"

func TestReviewStateEnterExitRestoresTokenInfoMatchRust(t *testing.T) {
	tokenInfo := "12K tokens"
	state := ReviewState{}

	enter := state.EnterReviewMode(&tokenInfo)
	if !enter.Entered || !state.IsReviewMode || !state.PreReviewTokenInfoSet || state.PreReviewTokenInfo == nil {
		t.Fatalf("enter result=%#v state=%#v", enter, state)
	}
	tokenInfo = "mutated"
	exit := state.ExitReviewMode()
	if !exit.Exited || exit.RestoreTokenInfo == nil || *exit.RestoreTokenInfo != "12K tokens" || exit.ClearTokenInfo {
		t.Fatalf("exit result = %#v", exit)
	}
	if state.IsReviewMode || state.PreReviewTokenInfoSet || state.PreReviewTokenInfo != nil {
		t.Fatalf("state should clear after exit: %#v", state)
	}
}

func TestReviewStateEnterExitRestoresNilTokenInfoMatchRust(t *testing.T) {
	state := ReviewState{}

	state.EnterReviewMode(nil)
	exit := state.ExitReviewMode()

	if !exit.ClearTokenInfo || exit.RestoreTokenInfo != nil {
		t.Fatalf("nil token snapshot should clear token info: %#v", exit)
	}
}

func TestReviewStateResetForThreadChangeMatchRust(t *testing.T) {
	tokenInfo := "tokens"
	state := ReviewState{
		RecentAutoReviewDenials: []string{"denied"},
		IsReviewMode:            true,
	}
	state.EnterReviewMode(&tokenInfo)

	state.ResetForThreadChange()

	if len(state.RecentAutoReviewDenials) != 0 || state.IsReviewMode || state.PreReviewTokenInfoSet || state.PreReviewTokenInfo != nil {
		t.Fatalf("reset state = %#v", state)
	}
}
