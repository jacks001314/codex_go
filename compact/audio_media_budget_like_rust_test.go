package compact

import (
	"strings"
	"testing"
)

func remoteFileAudioMessageForTest(id string, text string) Item {
	parts := []ContentPart{
		{Type: "input_text", Text: "<audio>"},
		{Type: "input_audio", FileID: "file_" + id},
		{Type: "input_text", Text: "</audio>"},
	}
	if text != "" {
		parts = append(parts, ContentPart{Type: "input_text", Text: text})
	}
	return Item{
		ID: id, Type: "message", Role: "user", Kind: "user_message", Text: text, Content: parts,
	}
}

// TestFileAudioChargedWithoutImageBudgetLikeRust pins Rust #59237: retained
// file audio consumes the retained-message budget even when the image budget is
// disabled, so an unmeasured 20-minute recording cannot ride along free.
func TestFileAudioChargedWithoutImageBudgetLikeRust(t *testing.T) {
	items := []Item{remoteFileAudioMessageForTest("audio", "listen")}
	if got := retainedMessageFileAudioCount(items[0]); got != 1 {
		t.Fatalf("file audio count = %d, want 1", got)
	}
	labelTokens := EstimateTextTokens("<audio>") + EstimateTextTokens("</audio>") + EstimateTextTokens("listen")
	if got := retainedMessageTokens(items[0], false, nil); got != FileAudioTokensEstimate+labelTokens {
		t.Fatalf("unmeasured tokens = %d, want %d", got, FileAudioTokensEstimate+labelTokens)
	}
	retained := retainedMessagesForRemoteCompaction(items, 100, retentionPolicy{})
	if len(retained) != 1 {
		t.Fatalf("retained = %#v", retained)
	}
	if retainedMessageFileAudioCount(retained[0]) != 0 {
		t.Fatalf("oversized audio group survived: %#v", retained[0].Content)
	}
	if retained[0].Text != "listen" {
		t.Fatalf("retained text = %q, want listen", retained[0].Text)
	}
}

// TestMeasuredFileAudioFitsRetainedBudgetLikeRust pins Rust #59268: measured
// durations replace the conservative maximum, so short recordings survive a
// budget that only fits five unmeasured files.
func TestMeasuredFileAudioFitsRetainedBudgetLikeRust(t *testing.T) {
	items := make([]Item, 0, 7)
	durations := FileAudioDurations{}
	for index := 0; index < 7; index++ {
		id := string(rune('a' + index))
		items = append(items, remoteFileAudioMessageForTest(id, ""))
		durations["file_"+id] = 0.5
	}
	unmeasured := retainedMessagesForRemoteCompactionWithAudio(items, RemoteRetainedMessageTokenBudget, retentionPolicy{}, nil)
	if len(unmeasured) != 5 {
		t.Fatalf("unmeasured retained %d messages, want 5", len(unmeasured))
	}
	measured := retainedMessagesForRemoteCompactionWithAudio(items, RemoteRetainedMessageTokenBudget, retentionPolicy{}, durations)
	if len(measured) != 7 {
		t.Fatalf("measured retained %d messages, want 7", len(measured))
	}
	if measured[0].ID != "a" || measured[6].ID != "g" {
		t.Fatalf("measured order = %q..%q", measured[0].ID, measured[6].ID)
	}
}

// TestFileAudioLabelsStayAtomicLikeRust pins that the audio part keeps its
// adjacent <audio>/</audio> labels when it fits the remaining budget.
func TestFileAudioLabelsStayAtomicLikeRust(t *testing.T) {
	message := remoteFileAudioMessageForTest("audio", strings.Repeat("w", 40))
	durations := FileAudioDurations{"file_audio": 100} // 1000 tokens
	retained := retainedMessagesForRemoteCompactionWithAudio([]Item{message}, 2000, retentionPolicy{}, durations)
	if len(retained) != 1 {
		t.Fatalf("retained = %#v", retained)
	}
	parts := retained[0].Content
	if len(parts) != 4 {
		t.Fatalf("parts = %#v, want labels, audio, text", parts)
	}
	if parts[0].Text != "<audio>" || parts[1].Type != "input_audio" || parts[2].Text != "</audio>" {
		t.Fatalf("audio group = %#v", parts[:3])
	}
}

// TestEstimateItemTokensWithAudioLikeRust pins the measured history estimate:
// file audio charges its duration budget, while inline audio keeps the decoded
// duration estimate.
func TestEstimateItemTokensWithAudioLikeRust(t *testing.T) {
	item := Item{
		Type: "message", Role: "user",
		Content: []ContentPart{{Type: "input_audio", FileID: "file_audio"}},
	}
	if got := EstimateItemTokens(&item); got != FileAudioTokensEstimate {
		t.Fatalf("unmeasured tokens = %d, want %d", got, FileAudioTokensEstimate)
	}
	durations := FileAudioDurations{"file_audio": 2}
	if got := EstimateItemTokensWithAudio(&item, durations); got != 20 {
		t.Fatalf("measured tokens = %d, want 20", got)
	}
}
