package chatgptapi

import (
	"encoding/json"
	"testing"
)

func TestCurrentPRDiff(t *testing.T) {
	var response GetTaskResponse
	if err := json.Unmarshal([]byte(`{
		"current_diff_task_turn": {
			"output_items": [
				{"type":"message","text":"ignored"},
				{"type":"pr","output_diff":{"diff":"diff --git a/file b/file"}}
			]
		}
	}`), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got := response.CurrentPRDiff(); got != "diff --git a/file b/file" {
		t.Fatalf("CurrentPRDiff() = %q", got)
	}
}
