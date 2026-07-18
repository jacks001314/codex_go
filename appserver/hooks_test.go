package appserver

import (
	"errors"
	"testing"
	"time"
)

func TestRegistryAddListAndClone(t *testing.T) {
	registry := NewHookRegistry()
	command := "go test"
	first := sampleMetadata("b", 20)
	second := sampleMetadata("a", 10)
	second.Command = &command
	if err := registry.Add("/repo", first); err != nil {
		t.Fatalf("Add(first) error = %v", err)
	}
	if err := registry.Add("/repo", second); err != nil {
		t.Fatalf("Add(second) error = %v", err)
	}
	registry.AddWarning("/repo", "warning")
	registry.AddError("/repo", HookErrorInfo{Path: "/repo/hooks.json", Message: "bad hook"})

	response := registry.List(&HookListParams{})
	if len(response.Data) != 1 || len(response.Data[0].Hooks) != 2 {
		t.Fatalf("List() = %+v", response)
	}
	if response.Data[0].Hooks[0].Key != "a" || response.Data[0].Hooks[1].Key != "b" {
		t.Fatalf("hooks order = %+v", response.Data[0].Hooks)
	}
	*response.Data[0].Hooks[0].Command = "mutated"
	again := registry.List(nil)
	if *again.Data[0].Hooks[0].Command != "go test" {
		t.Fatalf("List() leaked pointer mutation")
	}
}

func TestRegistryStartComplete(t *testing.T) {
	registry := NewHookRegistry()
	times := []time.Time{time.UnixMilli(1000), time.UnixMilli(1125)}
	registry.SetClock(func() time.Time {
		value := times[0]
		times = times[1:]
		return value
	})
	turnID := "turn-1"
	started, err := registry.Start("thread-1", &turnID, sampleMetadata("hook", 1), HookExecutionSync, HookScopeTurn)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if started.Run.Status != HookRunRunning || started.Run.StartedAt != 1000 {
		t.Fatalf("started = %+v", started)
	}
	message := "done"
	completed, err := registry.Complete("thread-1", &turnID, started.Run.ID, HookRunCompleted, []HookOutputEntry{{Kind: HookOutputContext, Text: "ok"}}, &message)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if completed.Run.Status != HookRunCompleted || completed.Run.CompletedAt == nil || *completed.Run.DurationMS != 125 {
		t.Fatalf("completed = %+v", completed)
	}
}

func TestRegistryValidation(t *testing.T) {
	registry := NewHookRegistry()
	if err := registry.Add("/repo", HookMetadata{}); !errors.Is(err, ErrInvalidHook) {
		t.Fatalf("Add(bad metadata) error = %v, want ErrInvalidHook", err)
	}
	if _, err := registry.Start("", nil, sampleMetadata("hook", 1), HookExecutionSync, HookScopeTurn); !errors.Is(err, ErrInvalidHook) {
		t.Fatalf("Start(empty thread) error = %v, want ErrInvalidHook", err)
	}
	if _, err := registry.Complete("thread", nil, "missing", HookRunFailed, nil, nil); !errors.Is(err, ErrInvalidHook) {
		t.Fatalf("Complete(missing run) error = %v, want ErrInvalidHook", err)
	}
}

func sampleMetadata(key string, order int64) HookMetadata {
	return HookMetadata{
		Key:          key,
		EventName:    HookEventPostToolUse,
		HandlerType:  HookHandlerCommand,
		SourcePath:   "/repo/hooks.json",
		Source:       HookSourceProject,
		DisplayOrder: order,
		Enabled:      true,
		TrustStatus:  HookTrustTrusted,
	}
}
