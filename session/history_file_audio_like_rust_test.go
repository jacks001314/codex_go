package session

import (
	"testing"

	"codex_go/utils"
)

// Mirrors Rust #60735: file-backed audio keeps its reference through persisted
// tool output and history blocks instead of becoming an inline or text part.
func TestFileAudioReferencesSurviveHistoryLikeRust(t *testing.T) {
	value := historyContentItemsValue([]utils.FunctionCallOutputContentItem{{
		Kind:   utils.ContentInputAudio,
		FileID: "file-clip",
	}})
	item, ok := value[0].(map[string]any)
	if !ok || item["type"] != "input_audio" || item["file_id"] != "file-clip" {
		t.Fatalf("history content item = %#v", value)
	}
	if _, ok := item["audio_url"]; ok {
		t.Fatalf("file audio also carried audio_url: %#v", item)
	}
	blocks := contentBlocksFromSessionItem(&Item{Content: []ContentPart{{
		Type:   "input_audio",
		FileID: "file-clip",
	}}}, "user", nil)
	if len(blocks) != 1 || blocks[0]["file_id"] != "file-clip" {
		t.Fatalf("history blocks = %#v", blocks)
	}
	if _, ok := blocks[0]["audio_url"]; ok {
		t.Fatalf("history block also carried audio_url: %#v", blocks[0])
	}
}
