package app

import "testing"

// Rust parity: codex-rs/tui/src/app/session_lifecycle.rs (#53895, upstream
// 4906ddf5b8). Upstream made should_attach_live_thread_for_selection an OR of two
// disjuncts (:774-782): the navigation entry is closed, or the cached channel is
// missing / is not the live attachment; and it deleted the
// `!contains_key && is_replay_only => "is no longer available"` branch so a
// persisted subagent thread with a saved transcript is resumed when selected.

func TestShouldAttachLiveThreadForSelectionFollowsRustLikeRust(t *testing.T) {
	cases := []struct {
		name        string
		hasChannel  bool
		channelLive bool
		entryExists bool
		entryClosed bool
		want        bool
	}{
		{"closed entry without a channel re-attaches", false, false, true, true, true},
		{"closed entry with a live channel re-attaches", true, true, true, true, true},
		{"open entry with a non-live channel re-attaches", true, false, true, false, true},
		{"open entry with a live channel is already attached", true, true, true, false, false},
		{"unknown entry without a channel re-attaches", false, false, false, false, true},
	}
	for _, tc := range cases {
		got := ShouldAttachLiveThreadForSelection(tc.hasChannel, tc.channelLive, tc.entryExists, tc.entryClosed)
		if got != tc.want {
			t.Fatalf("%s: ShouldAttachLiveThreadForSelection(%v, %v, %v, %v) = %v, want %v", tc.name, tc.hasChannel, tc.channelLive, tc.entryExists, tc.entryClosed, got, tc.want)
		}
	}
}

func TestSelectAgentThreadDecisionResumesClosedThreadsLikeRust(t *testing.T) {
	// A closed entry with a live channel still re-attaches: a close notification
	// queued by the previous runtime must not undo the new attachment (upstream
	// tests.rs assertion).
	reclosed := SelectAgentThreadDecision(AgentThreadSelectionInput{
		ThreadID:              rustThreadID2,
		LivenessAvailable:     true,
		EntryExists:           true,
		EntryIsClosed:         true,
		HasThreadEventChannel: true,
		ChannelIsLive:         true,
	})
	if !reclosed.Proceed || !reclosed.ShouldAttachLive || !reclosed.IsReplayOnly || reclosed.NoLongerAvailable || reclosed.ErrorMessage != "" {
		t.Fatalf("closed entry with a live channel = %#v, want a live re-attachment", reclosed)
	}

	// A closed entry whose transcript was saved must resume through the replay
	// flow rather than report "is no longer available".
	persisted := SelectAgentThreadDecision(AgentThreadSelectionInput{
		ThreadID:          rustThreadID2,
		LivenessAvailable: true,
		EntryExists:       true,
		EntryIsClosed:     true,
	})
	if !persisted.Proceed || !persisted.ShouldAttachLive || !persisted.IsReplayOnly || persisted.NoLongerAvailable || persisted.ErrorMessage != "" {
		t.Fatalf("persisted subagent decision = %#v, want a replay resume", persisted)
	}

	// An open entry with a replay-only channel re-attaches too (second disjunct).
	replayChannel := SelectAgentThreadDecision(AgentThreadSelectionInput{
		ThreadID:              rustThreadID3,
		LivenessAvailable:     true,
		EntryExists:           true,
		HasThreadEventChannel: true,
		ChannelIsLive:         false,
	})
	if !replayChannel.Proceed || !replayChannel.ShouldAttachLive || replayChannel.IsReplayOnly {
		t.Fatalf("replay-only channel decision = %#v, want a re-attach", replayChannel)
	}

	// Untouched path: an entry whose liveness could not be read is still gone.
	gone := SelectAgentThreadDecision(AgentThreadSelectionInput{ThreadID: rustThreadID1})
	if !gone.NoLongerAvailable || gone.ErrorMessage != "Agent thread "+rustThreadID1+" is no longer available." || gone.Proceed {
		t.Fatalf("unavailable thread decision = %#v", gone)
	}
}
