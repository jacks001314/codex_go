package realtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVoiceRecordingReplayMatchesGolden implements the djalign L2
// record-replay gate for the voice domain: a recorded realtime event stream is
// replayed through Go's canonical realtime history reducer, and the resulting
// item surface is frozen as a golden digest.
//
// Model text is reduced to a presence flag, matching the L2 normalization rule
// that model wording is not contract. The recording is derived from the Rust
// realtime protocol contract (the upstream checkout ships notification schemas,
// not a realtime session recording), so upstream schema or reducer drift both
// surface as a failing contract.
func TestVoiceRecordingReplayMatchesGolden(t *testing.T) {
	digest := replayVoiceRecording(t, filepath.Join("testdata", "voice_session.jsonl"))
	golden := []string{
		"realtimeSessionStarted|role=|presentation=|outcome=|text=absent",
		"transcriptSegment|role=user|presentation=|outcome=|text=present",
		"transcriptSegment|role=assistant|presentation=|outcome=|text=present",
		"bemItemPromoted|role=|presentation=wholeItem|outcome=|text=absent",
		"realtimeSessionClosed|role=|presentation=|outcome=ended|text=absent",
	}
	if len(digest) != len(golden) {
		t.Fatalf("replayed item digest drift\n got: %v\nwant: %v", digest, golden)
	}
	for index := range golden {
		if digest[index] != golden[index] {
			t.Fatalf("replayed item %d = %q, want %q", index, digest[index], golden[index])
		}
	}
}

type voiceReplayOp struct {
	Op               string `json:"op"`
	SessionID        string `json:"sessionId"`
	TurnID           string `json:"turnId"`
	ItemID           string `json:"itemId"`
	Role             string `json:"role"`
	Delta            string `json:"delta"`
	Text             string `json:"text"`
	Completed        *bool  `json:"completed"`
	PresentationKind string `json:"presentationKind"`
}

func replayVoiceRecording(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	content := strings.TrimPrefix(string(data), "\ufeff")
	state := &RealtimeHistoryState{}
	counter := 0
	state.SetIDGenerator(func() string {
		counter++
		return fmt.Sprintf("item-%d", counter)
	})
	digest := []string{}
	for lineNumber, line := range strings.Split(strings.TrimSpace(content), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var op voiceReplayOp
		if err := json.Unmarshal([]byte(line), &op); err != nil {
			t.Fatalf("line %d: %v", lineNumber+1, err)
		}
		var effects RealtimeEventEffects
		switch op.Op {
		case "startSession":
			effects = state.StartSession(op.SessionID)
		case "transcriptDelta":
			effects = state.TranscriptDelta(RealtimeTranscriptRole(op.Role), op.Delta)
		case "transcriptDone":
			effects = state.TranscriptDone(RealtimeTranscriptRole(op.Role), op.Text)
		case "bindTurn":
			state.BindTurnSession(op.TurnID)
			continue
		case "agentItem":
			completed := true
			if op.Completed != nil {
				completed = *op.Completed
			}
			effects = state.ObserveAgentItem(op.TurnID, op.ItemID, op.Text, completed)
		case "promote":
			effects = state.PromoteAgentItem(op.TurnID, op.ItemID, BemItemPresentation{
				Kind: BemItemPresentationKind(op.PresentationKind),
			})
		case "sealUserInput":
			effects = state.SealUserInput()
		case "sessionClosed":
			effects = state.SessionClosed()
		default:
			t.Fatalf("line %d: unknown voice replay op %q", lineNumber+1, op.Op)
		}
		for _, item := range effects.Items {
			digest = append(digest, voiceItemDigest(item))
		}
	}
	return digest
}

func voiceItemDigest(item RealtimeItem) string {
	text := "absent"
	if strings.TrimSpace(item.Content.Text) != "" {
		text = "present"
	}
	return fmt.Sprintf("%s|role=%s|presentation=%s|outcome=%s|text=%s",
		item.Content.Kind, item.Content.Role, item.Content.Presentation.Kind, item.Content.Outcome, text)
}
