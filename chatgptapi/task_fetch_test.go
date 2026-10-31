package chatgptapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetTaskFetchesChatGPTBackendPath(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"current_diff_task_turn":{"output_items":[{"type":"pr","output_diff":{"diff":"diff --git a/x b/x"}}]}}`))
	}))
	defer server.Close()

	client := NewCloudClientWithoutRedirects(&CloudClientOptions{BaseURL: server.URL + "/backend-api"})
	task, err := GetTask(context.Background(), client, "task-123")
	if err != nil {
		t.Fatalf("GetTask error = %v", err)
	}
	if gotPath != "/backend-api/wham/tasks/task-123" {
		t.Fatalf("path = %q", gotPath)
	}
	if task.CurrentDiffTaskTurn == nil || len(task.CurrentDiffTaskTurn.OutputItems) != 1 {
		t.Fatalf("task = %#v", task)
	}
	if diff := task.CurrentDiffTaskTurn.OutputItems[0].OutputDiff; diff == nil || diff.Diff == nil || *diff.Diff != "diff --git a/x b/x" {
		t.Fatalf("output_diff = %#v", task.CurrentDiffTaskTurn.OutputItems[0].OutputDiff)
	}
}

func TestGetTaskRequiresClient(t *testing.T) {
	if _, err := GetTask(context.Background(), nil, "task-123"); err == nil {
		t.Fatal("GetTask(nil) error = nil")
	}
}
