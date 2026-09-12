package realtime

import (
	"encoding/json"
	"fmt"
	"testing"
)

func newTestHistory() *RealtimeHistoryState {
	history := &RealtimeHistoryState{}
	counter := 0
	history.SetIDGenerator(func() string {
		counter++
		return fmt.Sprintf("item-%d", counter)
	})
	return history
}

func TestRealtimeItemWireShapeMatchesRust(t *testing.T) {
	tests := []struct {
		name string
		item RealtimeItem
		want string
	}{
		{
			name: "session started",
			item: RealtimeItem{ID: "i1", RealtimeSessionID: "s1", Content: RealtimeItemContent{Kind: RealtimeItemKindSessionStarted}},
			want: `{"id":"i1","realtimeSessionId":"s1","type":"realtimeSessionStarted"}`,
		},
		{
			name: "transcript segment",
			item: RealtimeItem{
				ID:                "i2",
				RealtimeSessionID: "s1",
				Content: RealtimeItemContent{
					Kind: RealtimeItemKindTranscriptSegment,
					Role: RealtimeTranscriptRoleAssistant,
					Text: "line\nnext",
				},
			},
			want: `{"id":"i2","realtimeSessionId":"s1","type":"transcriptSegment","role":"assistant","text":"line\nnext"}`,
		},
		{
			name: "bem item promoted",
			item: RealtimeItem{
				ID:                "i3",
				RealtimeSessionID: "s1",
				Content: RealtimeItemContent{
					Kind:         RealtimeItemKindBemItemPromoted,
					TurnID:       "t1",
					ItemID:       "a1",
					Presentation: BemItemPresentation{Kind: BemPresentationWholeItem},
				},
			},
			want: `{"id":"i3","realtimeSessionId":"s1","type":"bemItemPromoted","turnId":"t1","itemId":"a1","presentation":{"type":"wholeItem"}}`,
		},
		{
			name: "inline visualization",
			item: RealtimeItem{
				ID:                "i4",
				RealtimeSessionID: "s1",
				Content: RealtimeItemContent{
					Kind:         RealtimeItemKindBemItemPromoted,
					TurnID:       "t1",
					ItemID:       "a2",
					Presentation: BemItemPresentation{Kind: BemPresentationInlineVisualization, Index: 2},
				},
			},
			want: `{"id":"i4","realtimeSessionId":"s1","type":"bemItemPromoted","turnId":"t1","itemId":"a2","presentation":{"type":"inlineVisualization","index":2}}`,
		},
		{
			name: "session closed",
			item: RealtimeItem{
				ID:                "i5",
				RealtimeSessionID: "s1",
				Content:           RealtimeItemContent{Kind: RealtimeItemKindSessionClosed, Outcome: RealtimeSessionOutcomeFailed},
			},
			want: `{"id":"i5","realtimeSessionId":"s1","type":"realtimeSessionClosed","outcome":"failed"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.item)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != test.want {
				t.Fatalf("wire = %s, want %s", encoded, test.want)
			}
			var decoded RealtimeItem
			if err := json.Unmarshal([]byte(test.want), &decoded); err != nil {
				t.Fatalf("decode %s: %v", test.want, err)
			}
			if decoded.ID != test.item.ID || decoded.RealtimeSessionID != test.item.RealtimeSessionID {
				t.Fatalf("decoded identity = %#v", decoded)
			}
			if decoded.Content != test.item.Content {
				t.Fatalf("decoded content = %#v, want %#v", decoded.Content, test.item.Content)
			}
			reencoded, err := json.Marshal(decoded)
			if err != nil {
				t.Fatal(err)
			}
			if string(reencoded) != test.want {
				t.Fatalf("re-encode = %s, want %s", reencoded, test.want)
			}
		})
	}
}

func TestRealtimeItemRejectsMalformedWireInput(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "missing id", raw: `{"realtimeSessionId":"s","type":"realtimeSessionStarted"}`},
		{name: "missing session", raw: `{"id":"i","type":"realtimeSessionStarted"}`},
		{name: "missing type", raw: `{"id":"i","realtimeSessionId":"s"}`},
		{name: "unknown field", raw: `{"id":"i","realtimeSessionId":"s","type":"realtimeSessionStarted","extra":1}`},
		{name: "unknown kind", raw: `{"id":"i","realtimeSessionId":"s","type":"other"}`},
		{name: "segment missing role", raw: `{"id":"i","realtimeSessionId":"s","type":"transcriptSegment","text":"x"}`},
		{name: "segment unknown role", raw: `{"id":"i","realtimeSessionId":"s","type":"transcriptSegment","role":"robot","text":"x"}`},
		{name: "segment missing text", raw: `{"id":"i","realtimeSessionId":"s","type":"transcriptSegment","role":"user"}`},
		{name: "promotion missing presentation", raw: `{"id":"i","realtimeSessionId":"s","type":"bemItemPromoted","turnId":"t","itemId":"a"}`},
		{name: "promotion unknown presentation", raw: `{"id":"i","realtimeSessionId":"s","type":"bemItemPromoted","turnId":"t","itemId":"a","presentation":{"type":"other"}}`},
		{name: "visualization missing index", raw: `{"id":"i","realtimeSessionId":"s","type":"bemItemPromoted","turnId":"t","itemId":"a","presentation":{"type":"inlineVisualization"}}`},
		{name: "closed unknown outcome", raw: `{"id":"i","realtimeSessionId":"s","type":"realtimeSessionClosed","outcome":"other"}`},
		{name: "closed missing outcome", raw: `{"id":"i","realtimeSessionId":"s","type":"realtimeSessionClosed"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var decoded RealtimeItem
			if err := json.Unmarshal([]byte(test.raw), &decoded); err == nil {
				t.Fatalf("malformed item decoded: %#v", decoded)
			}
		})
	}
}

func TestRealtimeItemRejectsInvalidContent(t *testing.T) {
	tests := []struct {
		name    string
		content RealtimeItemContent
	}{
		{name: "unknown kind", content: RealtimeItemContent{Kind: "other"}},
		{name: "missing role", content: RealtimeItemContent{Kind: RealtimeItemKindTranscriptSegment}},
		{name: "missing outcome", content: RealtimeItemContent{Kind: RealtimeItemKindSessionClosed}},
		{name: "unknown presentation", content: RealtimeItemContent{
			Kind:         RealtimeItemKindBemItemPromoted,
			Presentation: BemItemPresentation{Kind: "other"},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := json.Marshal(test.content); err == nil {
				t.Fatal("invalid content encoded successfully")
			}
		})
	}
}

func TestRealtimeHistorySessionFlow(t *testing.T) {
	history := newTestHistory()
	start := history.StartSession("s1")
	if len(start.Items) != 1 || start.Items[0].Content.Kind != RealtimeItemKindSessionStarted {
		t.Fatalf("start items = %#v", start.Items)
	}
	if history.StartSession("s1").Empty() == false {
		t.Fatal("resuming the same session must not duplicate the boundary")
	}

	history.SetActiveTurn("turn-1")
	history.BindTurnSession("turn-1")

	delta := history.TranscriptDelta(RealtimeTranscriptRoleUser, "hello")
	stream := delta.TranscriptStream
	if stream == nil || stream.ItemID == "" {
		t.Fatalf("first delta stream = %#v", stream)
	}
	if stream.StartedItem == nil || stream.StartedItem.Content.Kind != RealtimeItemKindTranscriptSegment {
		t.Fatalf("started item = %#v", stream.StartedItem)
	}
	if stream.StartedItem.Content.Text != "" {
		t.Fatalf("started item must stream empty text, got %q", stream.StartedItem.Content.Text)
	}
	if stream.Delta != "hello" {
		t.Fatalf("stream delta = %q", stream.Delta)
	}

	second := history.TranscriptDelta(RealtimeTranscriptRoleUser, " world")
	if second.TranscriptStream == nil || second.TranscriptStream.StartedItem != nil {
		t.Fatalf("continuation stream = %#v", second.TranscriptStream)
	}
	if second.TranscriptStream.ItemID != stream.ItemID {
		t.Fatalf("continuation item = %q, want %q", second.TranscriptStream.ItemID, stream.ItemID)
	}

	done := history.TranscriptDone(RealtimeTranscriptRoleUser, "hello world")
	if len(done.Items) != 1 {
		t.Fatalf("done items = %#v", done.Items)
	}
	segment := done.Items[0]
	if segment.Content.Kind != RealtimeItemKindTranscriptSegment ||
		segment.Content.Text != "hello world" ||
		segment.Content.Role != RealtimeTranscriptRoleUser {
		t.Fatalf("sealed segment = %#v", segment)
	}
	if segment.ID != stream.ItemID {
		t.Fatalf("sealed id = %q, want %q", segment.ID, stream.ItemID)
	}

	closed := history.SessionClosed()
	if len(closed.Items) != 1 || closed.Items[0].Content.Kind != RealtimeItemKindSessionClosed {
		t.Fatalf("closed items = %#v", closed.Items)
	}
	if closed.Items[0].Content.Outcome != RealtimeSessionOutcomeEnded {
		t.Fatalf("outcome = %q", closed.Items[0].Content.Outcome)
	}
	if !history.SessionClosed().Empty() {
		t.Fatal("closing an inactive session must produce nothing")
	}
}

func TestRealtimeHistoryFailureOutcome(t *testing.T) {
	history := newTestHistory()
	history.StartSession("s1")
	history.MarkFailed()
	closed := history.SessionClosed()
	if len(closed.Items) != 1 || closed.Items[0].Content.Outcome != RealtimeSessionOutcomeFailed {
		t.Fatalf("closed items = %#v", closed.Items)
	}
}

func TestRealtimeHistorySealsUserInputAcrossRoles(t *testing.T) {
	history := newTestHistory()
	history.StartSession("s1")
	history.SetActiveTurn("turn-1")
	history.BindTurnSession("turn-1")

	// The assistant speaks first, so the assistant segment seals first.
	history.TranscriptDelta(RealtimeTranscriptRoleAssistant, "ready")
	history.TranscriptDelta(RealtimeTranscriptRoleUser, "go")

	sealed := history.SealUserInput()
	if len(sealed.Items) != 2 {
		t.Fatalf("sealed items = %#v", sealed.Items)
	}
	if sealed.Items[0].Content.Role != RealtimeTranscriptRoleAssistant ||
		sealed.Items[1].Content.Role != RealtimeTranscriptRoleUser {
		t.Fatalf("seal order = %#v", sealed.Items)
	}
	if sealed.Order != RealtimeOrderBeforeEvent {
		t.Fatalf("seal order flag = %v", sealed.Order)
	}
	// A continuation keeps both roles active without repeating the text.
	if repeated := history.SealUserInput(); len(repeated.Items) != 0 {
		t.Fatalf("empty continuation produced %#v", repeated.Items)
	}
}

func TestRealtimeHistoryPromotesAgentItems(t *testing.T) {
	history := newTestHistory()
	history.StartSession("s1")
	history.SetActiveTurn("turn-1")
	history.BindTurnSession("turn-1")

	whole := history.PromoteAgentItem("turn-1", "image-1", BemItemPresentation{Kind: BemPresentationWholeItem})
	if len(whole.Items) != 1 || whole.Items[0].Content.Kind != RealtimeItemKindBemItemPromoted {
		t.Fatalf("whole item promotion = %#v", whole.Items)
	}
	if whole.Items[0].Content.Presentation.Kind != BemPresentationWholeItem ||
		whole.Items[0].Content.ItemID != "image-1" ||
		whole.Items[0].Content.TurnID != "turn-1" {
		t.Fatalf("whole item content = %#v", whole.Items[0].Content)
	}
	if repeated := history.PromoteAgentItem("turn-1", "image-1", BemItemPresentation{Kind: BemPresentationWholeItem}); len(repeated.Items) != 0 {
		t.Fatalf("duplicate promotion produced %#v", repeated.Items)
	}

	markdown := history.ObserveAgentItem("turn-1", "agent-1", "::codex-realtime-inline{}\nbody", false)
	if len(markdown.Items) != 1 || markdown.Items[0].Content.Presentation.Kind != BemPresentationInlineMarkdown {
		t.Fatalf("inline markdown promotion = %#v", markdown.Items)
	}

	visual := history.ObserveAgentItem("turn-1", "agent-2", "text\n::codex-inline-vis{1}\nmore", false)
	if len(visual.Items) != 1 || visual.Items[0].Content.Presentation.Kind != BemPresentationInlineVisualization {
		t.Fatalf("inline visualization promotion = %#v", visual.Items)
	}
	if visual.Items[0].Content.Presentation.Index != 0 {
		t.Fatalf("visualization index = %d", visual.Items[0].Content.Presentation.Index)
	}

	// Fenced directives and content before the session was bound never promote.
	fenced := history.ObserveAgentItem("turn-1", "agent-3", "```\n::codex-inline-vis{0}\n```", false)
	if len(fenced.Items) != 0 {
		t.Fatalf("fenced promotion = %#v", fenced.Items)
	}
	unbound := history.ObserveAgentItem("turn-unknown", "agent-4", "::codex-realtime-inline{}\nbody", false)
	if len(unbound.Items) != 0 {
		t.Fatalf("unbound promotion = %#v", unbound.Items)
	}
}

func TestRealtimeHistoryHandoffBindsNextTurn(t *testing.T) {
	history := newTestHistory()
	history.StartSession("s1")
	history.NoteHandoffRequested()
	history.BindTurnSession("turn-handoff")
	if promoted := history.PromoteAgentItem("turn-handoff", "item-1", BemItemPresentation{Kind: BemPresentationWholeItem}); len(promoted.Items) != 1 {
		t.Fatalf("handoff-bound promotion = %#v", promoted.Items)
	}
	if promoted := history.PromoteAgentItem("turn-other", "item-2", BemItemPresentation{Kind: BemPresentationWholeItem}); len(promoted.Items) != 0 {
		t.Fatalf("unbound promotion = %#v", promoted.Items)
	}
}

func TestRealtimeHistoryNotificationsMatchRustDelivery(t *testing.T) {
	started := RealtimeItem{ID: "seg", Content: RealtimeItemContent{Kind: RealtimeItemKindTranscriptSegment, Role: RealtimeTranscriptRoleUser}}
	effects := RealtimeEventEffects{
		TranscriptStream: &RealtimeTranscriptStream{StartedItem: &started, ItemID: "seg", Delta: "hi"},
		Items: []RealtimeItem{
			{ID: "seg", Content: RealtimeItemContent{Kind: RealtimeItemKindTranscriptSegment, Role: RealtimeTranscriptRoleUser, Text: "hi"}},
			{ID: "bem", Content: RealtimeItemContent{Kind: RealtimeItemKindBemItemPromoted}},
		},
	}
	notifications := historyItemNotifications("thread-a", effects)
	got := make([]NotificationMethod, 0, len(notifications))
	for _, notification := range notifications {
		got = append(got, notification.Method)
	}
	want := []NotificationMethod{
		NotificationItemStarted,
		NotificationItemTranscriptDelta,
		NotificationItemCompleted,
		NotificationItemStarted,
		NotificationItemCompleted,
	}
	if len(got) != len(want) {
		t.Fatalf("methods = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("methods = %#v, want %#v", got, want)
		}
	}
	if notifications[1].Params.(ItemTranscriptDeltaNotification).Delta != "hi" {
		t.Fatalf("delta notification = %#v", notifications[1].Params)
	}
	if historyItemNotifications("thread-a", RealtimeEventEffects{}) != nil {
		t.Fatal("empty effects must produce no notifications")
	}
}

func TestManagerObservesTimelineEvents(t *testing.T) {
	manager := NewManager()
	history := manager.historyFor("thread-a")
	history.StartSession("session-1")
	history.SetActiveTurn("turn-1")
	history.BindTurnSession("turn-1")

	delta := manager.observeTimelineEvent("thread-a", Event{Type: "input_transcript.delta", Delta: "hi"})
	methods := make([]NotificationMethod, 0, len(delta))
	for _, notification := range delta {
		methods = append(methods, notification.Method)
	}
	if len(methods) != 2 || methods[0] != NotificationItemStarted || methods[1] != NotificationItemTranscriptDelta {
		t.Fatalf("delta methods = %#v", methods)
	}

	done := manager.observeTimelineEvent("thread-a", Event{Type: "input_transcript.done", Text: "hi"})
	if len(done) != 1 || done[0].Method != NotificationItemCompleted {
		t.Fatalf("done methods = %#v", done)
	}
	if done[0].Params.(ItemCompletedNotification).Item.Content.Text != "hi" {
		t.Fatalf("done item = %#v", done[0].Params)
	}

	if events := manager.observeTimelineEvent("thread-a", Event{Type: "handoff.requested"}); len(events) != 0 {
		t.Fatalf("handoff produced %#v", events)
	}
	if events := manager.observeTimelineEvent("thread-a", Event{Type: "error"}); len(events) != 0 {
		t.Fatalf("error produced %#v", events)
	}
	closed := manager.takeSessionClosedNotifications("thread-a")
	if len(closed) != 2 || closed[1].Params.(ItemCompletedNotification).Item.Content.Outcome != RealtimeSessionOutcomeFailed {
		t.Fatalf("failed session close = %#v", closed)
	}
}
