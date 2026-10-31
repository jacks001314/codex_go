package chatgptapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// GetTask fetches a Codex task turn bundle from the ChatGPT backend
// (`GET /wham/tasks/<id>`, Rust chatgpt::get_task). The task id is interpolated
// verbatim, exactly like Rust's `format!("/wham/tasks/{task_id}")`.
func GetTask(ctx context.Context, client *CloudClient, taskID string) (*GetTaskResponse, error) {
	if client == nil {
		return nil, errors.New("cloud client is nil")
	}
	id := strings.TrimSpace(taskID)
	if id == "" {
		return nil, errors.New("task id is required")
	}
	var response GetTaskResponse
	if err := client.doJSON(ctx, http.MethodGet, client.apiPath("tasks", id), nil, nil, &response); err != nil {
		return nil, err
	}
	return &response, nil
}
