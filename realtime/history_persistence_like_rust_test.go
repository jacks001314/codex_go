package realtime

// Mirrors Rust #41924: canonical realtime items reach the host store in event
// order, ephemeral threads publish no history, and typed input seals segments
// unless it is a realtime handoff delegation.

import "testing"

func TestRealtimeItemRolloutJSONMatchesCoreWireShapeLikeRust(t *testing.T) {
	cases := []struct {
		item RealtimeItem
		want string
	}{
		{
			item: RealtimeItem{ID: "a", RealtimeSessionID: "s", Content: RealtimeItemContent{Kind: RealtimeItemKindSessionStarted}},
			want: `{"id":"a","realtime_session_id":"s","type":"realtime_session_started"}`,
		},
		{
			item: RealtimeItem{ID: "b", RealtimeSessionID: "s", Content: RealtimeItemContent{Kind: RealtimeItemKindTranscriptSegment, Role: RealtimeTranscriptRoleUser, Text: "hi"}},
			want: `{"id":"b","realtime_session_id":"s","type":"transcript_segment","role":"user","text":"hi"}`,
		},
		{
			item: RealtimeItem{ID: "c", RealtimeSessionID: "s", Content: RealtimeItemContent{
				Kind: RealtimeItemKindBemItemPromoted, TurnID: "t", ItemID: "i",
				Presentation: BemItemPresentation{Kind: BemPresentationInlineVisualization, Index: 2},
			}},
			want: `{"id":"c","realtime_session_id":"s","type":"bem_item_promoted","turn_id":"t","item_id":"i","presentation":{"type":"inline_visualization","index":2}}`,
		},
		{
			item: RealtimeItem{ID: "d", RealtimeSessionID: "s", Content: RealtimeItemContent{Kind: RealtimeItemKindSessionClosed, Outcome: RealtimeSessionOutcomeFailed}},
			want: `{"id":"d","realtime_session_id":"s","type":"realtime_session_closed","outcome":"failed"}`,
		},
	}
	for _, testCase := range cases {
		encoded, err := testCase.item.MarshalRolloutJSON()
		if err != nil {
			t.Fatalf("MarshalRolloutJSON(%s) error = %v", testCase.item.ID, err)
		}
		if string(encoded) != testCase.want {
			t.Fatalf("MarshalRolloutJSON(%s) = %s, want %s", testCase.item.ID, encoded, testCase.want)
		}
		var decoded RealtimeItem
		if err := decoded.UnmarshalRolloutJSON(encoded); err != nil {
			t.Fatalf("UnmarshalRolloutJSON(%s) error = %v", testCase.item.ID, err)
		}
		if decoded != testCase.item {
			t.Fatalf("round trip %s = %#v, want %#v", testCase.item.ID, decoded, testCase.item)
		}
	}
}

func TestManagerPersistsCanonicalHistoryInEventOrderLikeRust(t *testing.T) {
	manager := NewManager()
	var persisted [][]RealtimeItem
	manager.SetHistoryItemSink(func(threadID string, items []RealtimeItem) {
		if threadID != "thread-a" {
			t.Fatalf("sink thread = %q", threadID)
		}
		persisted = append(persisted, append([]RealtimeItem(nil), items...))
	})

	history := manager.historyFor("thread-a")
	if effects := history.StartSession("session-1"); !effects.Empty() {
		manager.publishHistoryEffects("thread-a", effects)
	}
	manager.observeTimelineEvent("thread-a", Event{Type: "input_transcript.delta", Delta: "hi"})
	manager.observeTimelineEvent("thread-a", Event{Type: "input_transcript.done", Text: "hi"})
	manager.observeTimelineEvent("thread-a", Event{Type: "handoff.requested"})
	manager.takeSessionClosedNotifications("thread-a")

	var kinds []RealtimeItemKind
	for _, batch := range persisted {
		for _, item := range batch {
			kinds = append(kinds, item.Content.Kind)
		}
	}
	want := []RealtimeItemKind{
		RealtimeItemKindSessionStarted,
		RealtimeItemKindTranscriptSegment,
		RealtimeItemKindSessionClosed,
	}
	if len(kinds) != len(want) {
		t.Fatalf("persisted kinds = %v, want %v", kinds, want)
	}
	for index := range want {
		if kinds[index] != want[index] {
			t.Fatalf("persisted kinds = %v, want %v", kinds, want)
		}
	}
	if persisted[1][0].Content.Text != "hi" {
		t.Fatalf("persisted transcript = %#v", persisted[1][0].Content)
	}
}

func TestManagerSkipsHistoryForEphemeralThreadsLikeRust(t *testing.T) {
	manager := NewManager()
	manager.SetHistoryEnabled(func(threadID string) bool { return threadID != "ephemeral" })
	persisted := 0
	manager.SetHistoryItemSink(func(string, []RealtimeItem) { persisted++ })
	history := manager.historyFor("ephemeral")

	if notifications := manager.publishHistoryEffects("ephemeral", history.StartSession("session-1")); len(notifications) != 0 {
		t.Fatalf("ephemeral start notifications = %#v", notifications)
	}
	if notifications := manager.observeTimelineEvent("ephemeral", Event{Type: "input_transcript.delta", Delta: "hi"}); len(notifications) != 0 {
		t.Fatalf("ephemeral delta notifications = %#v", notifications)
	}
	if notifications := manager.takeSessionClosedNotifications("ephemeral"); len(notifications) != 0 {
		t.Fatalf("ephemeral close notifications = %#v", notifications)
	}
	if persisted != 0 {
		t.Fatalf("ephemeral sink calls = %d, want 0", persisted)
	}
}

func TestSealUserInputSkipsRealtimeDelegationLikeRust(t *testing.T) {
	manager := NewManager()
	var persisted []RealtimeItem
	manager.SetHistoryItemSink(func(_ string, items []RealtimeItem) {
		persisted = append(persisted, items...)
	})
	history := manager.historyFor("thread-a")
	manager.publishHistoryEffects("thread-a", history.StartSession("session-1"))
	manager.observeTimelineEvent("thread-a", Event{Type: "input_transcript.delta", Delta: "spoken"})

	if notifications := manager.SealRealtimeUserInputForPrompt("thread-a", "<realtime_delegation>\n  <input>x</input>\n</realtime_delegation>"); len(notifications) != 0 {
		t.Fatalf("delegation seal notifications = %#v", notifications)
	}
	before := len(persisted)
	if notifications := manager.SealRealtimeUserInputForPrompt("thread-a", "typed follow-up"); len(notifications) != 1 {
		t.Fatalf("typed seal notifications = %#v", notifications)
	}
	if len(persisted) != before+1 || persisted[len(persisted)-1].Content.Text != "spoken" {
		t.Fatalf("sealed items = %#v", persisted[before:])
	}
}
