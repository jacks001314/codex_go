package state

import (
	"errors"
	"testing"
	"time"
)

func TestTaskRegistryStartCompletesTask(t *testing.T) {
	registry := NewTaskRegistry()
	now := fixedTaskTime()
	registry.SetClock(func() time.Time { return now })
	registry.Register(TaskRegular, func(ctx *TaskContext) (string, error) {
		if ctx.TaskID != "task-a" || ctx.Kind != TaskRegular {
			t.Fatalf("context = %+v", ctx)
		}
		if err := ctx.CheckCancelled(); err != nil {
			return "", err
		}
		return "done", nil
	})

	task, err := registry.Start("task-a", TaskRegular, map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if task.Status != TaskStatusCompleted || task.Result != "done" {
		t.Fatalf("task = %+v", task)
	}
	if task.SpanName != "session_task.regular" {
		t.Fatalf("span = %s", task.SpanName)
	}
}

func TestTaskRegistryStartFails(t *testing.T) {
	registry := NewTaskRegistry()
	registry.Register(TaskReview, func(ctx *TaskContext) (string, error) {
		return "", errors.New("boom")
	})
	task, err := registry.Start("task-a", TaskReview, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if task.Status != TaskStatusFailed || task.Error != "boom" {
		t.Fatalf("task = %+v", task)
	}
	if _, err := registry.Start("task-a", TaskReview, nil); !errors.Is(err, ErrTaskAlreadyRunning) {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestTaskRegistryCancelBeforeRunnerFinishes(t *testing.T) {
	registry := NewTaskRegistry()
	registry.Register(TaskCompact, func(ctx *TaskContext) (string, error) {
		ctx.Cancel.Cancel("interrupted")
		return "", ctx.CheckCancelled()
	})
	task, err := registry.Start("compact-a", TaskCompact, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if task.Status != TaskStatusCancelled || task.Error != "interrupted" {
		t.Fatalf("task = %+v", task)
	}
}

func TestTaskRegistryCancelRunningTaskRecord(t *testing.T) {
	registry := NewTaskRegistry()
	task := &Task{ID: "task-a", Kind: TaskRegular, Status: TaskStatusRunning, Cancel: NewTaskCancelToken(), CreatedAt: fixedTaskTime()}
	registry.tasks["task-a"] = task
	cancelled, err := registry.Cancel("task-a", "user")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.Status != TaskStatusCancelled || !task.Cancel.Cancelled() {
		t.Fatalf("cancelled = %+v", cancelled)
	}
	if _, err := registry.Cancel("missing", "user"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("expected missing task, got %v", err)
	}
}

func TestTaskRegistryListSortedAndFiltered(t *testing.T) {
	registry := NewTaskRegistry()
	registry.tasks["b"] = &Task{ID: "b", Kind: TaskReview, Status: TaskStatusFailed, CreatedAt: fixedTaskTime().Add(time.Second)}
	registry.tasks["a"] = &Task{ID: "a", Kind: TaskRegular, Status: TaskStatusCompleted, CreatedAt: fixedTaskTime()}
	registry.tasks["c"] = &Task{ID: "c", Kind: TaskCompact, Status: TaskStatusCompleted, CreatedAt: fixedTaskTime().Add(2 * time.Second)}
	all := registry.List("")
	if len(all) != 3 || all[0].ID != "a" || all[2].ID != "c" {
		t.Fatalf("all = %+v", all)
	}
	completed := registry.List(TaskStatusCompleted)
	if len(completed) != 2 || completed[0].ID != "a" || completed[1].ID != "c" {
		t.Fatalf("completed = %+v", completed)
	}
}

func TestTaskMetricsAndCompactMetric(t *testing.T) {
	metrics := NewTaskMetrics()
	metrics.SetClock(func() time.Time { return fixedTaskTime() })
	EmitTaskCompactMetric(metrics, "remote_v2", true)
	records := metrics.Records()
	if len(records) != 1 {
		t.Fatalf("records = %+v", records)
	}
	record := records[0]
	if record.Name != TaskCompactMetricName || record.Inc != 1 {
		t.Fatalf("record = %+v", record)
	}
	if record.Tags["type"] != "remote_v2" || record.Tags["manual"] != "true" {
		t.Fatalf("tags = %#v", record.Tags)
	}
}

func TestTaskCancelToken(t *testing.T) {
	token := NewTaskCancelToken()
	if token.Cancelled() {
		t.Fatal("new token should not be cancelled")
	}
	token.Cancel("stop")
	if !token.Cancelled() || token.Reason() != "stop" {
		t.Fatalf("token reason = %q", token.Reason())
	}
}

func fixedTaskTime() time.Time {
	return time.Date(2026, 6, 29, 8, 0, 0, 0, time.UTC)
}
