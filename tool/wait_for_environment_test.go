package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

type environmentWaiterFunc func(context.Context, string) (EnvironmentStatus, string, error)

func (f environmentWaiterFunc) Status(ctx context.Context, environmentID string) (EnvironmentStatus, string, error) {
	return f(ctx, environmentID)
}

func TestWaitForEnvironmentHandlerWaitsUntilReadyLikeRust(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	handler := NewWaitForEnvironmentHandler(environmentWaiterFunc(func(context.Context, string) (EnvironmentStatus, string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return EnvironmentStatusPending, "", nil
		}
		return EnvironmentStatusReady, "", nil
	}), []string{"env-1"}, nil)
	handler.pollInterval = time.Millisecond

	output, err := handler.Execute(context.Background(), &Invocation{Payload: Payload{Kind: PayloadFunction, Arguments: `{"environment_id":"env-1"}`}})
	if err != nil {
		t.Fatal(err)
	}
	if !output.Success || output.Data["environment_id"] != "env-1" || output.Data["status"] != "ready" {
		t.Fatalf("output = %#v", output)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("status calls = %d, want 2", calls)
	}
}

func TestWaitForEnvironmentHandlerRejectsUnselectedAndStopsOnFailureOrCancellation(t *testing.T) {
	handler := NewWaitForEnvironmentHandler(environmentWaiterFunc(func(context.Context, string) (EnvironmentStatus, string, error) {
		return EnvironmentStatusDisconnected, "startup failed", nil
	}), []string{"env-1"}, nil)
	_, err := handler.Execute(context.Background(), &Invocation{Payload: Payload{Kind: PayloadFunction, Arguments: `{"environment_id":"env-2"}`}})
	if err == nil || !strings.Contains(err.Error(), "neither ready nor starting") {
		t.Fatalf("unselected error = %v", err)
	}
	_, err = handler.Execute(context.Background(), &Invocation{Payload: Payload{Kind: PayloadFunction, Arguments: `{"environment_id":"env-1"}`}})
	if err == nil || !strings.Contains(err.Error(), "failed to start") {
		t.Fatalf("failed error = %v", err)
	}

	pending := NewWaitForEnvironmentHandler(environmentWaiterFunc(func(context.Context, string) (EnvironmentStatus, string, error) {
		return EnvironmentStatusPending, "", nil
	}), []string{"env-1"}, nil)
	pending.pollInterval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = pending.Execute(ctx, &Invocation{Payload: Payload{Kind: PayloadFunction, Arguments: `{"environment_id":"env-1"}`}})
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("cancel error = %v", err)
	}
}

// Rust #44277: report the environment startup failure reason to the model,
// bounded to 256 bytes at a UTF-8 boundary.
func TestWaitForEnvironmentHandlerReportsFailureReason(t *testing.T) {
	invocation := func() *Invocation {
		return &Invocation{Payload: Payload{Kind: PayloadFunction, Arguments: `{"environment_id":"env-1"}`}}
	}
	handler := NewWaitForEnvironmentHandler(environmentWaiterFunc(func(context.Context, string) (EnvironmentStatus, string, error) {
		return EnvironmentStatusDisconnected, "repository is empty", nil
	}), []string{"env-1"}, nil)
	_, err := handler.Execute(context.Background(), invocation())
	if err == nil || !strings.Contains(err.Error(), "Environment `env-1` failed to start: repository is empty") {
		t.Fatalf("failure reason = %v", err)
	}

	long := strings.Repeat("\u00e9", 400) // 800 bytes, a rune boundary at 256
	truncating := NewWaitForEnvironmentHandler(environmentWaiterFunc(func(context.Context, string) (EnvironmentStatus, string, error) {
		return EnvironmentStatusDisconnected, long, nil
	}), []string{"env-1"}, nil)
	_, err = truncating.Execute(context.Background(), invocation())
	if err == nil {
		t.Fatal("expected failure")
	}
	reason := strings.TrimPrefix(err.Error(), "Environment `env-1` failed to start: ")
	if len(reason) != maxWaitForEnvironmentFailureBytes || !utf8.ValidString(reason) {
		t.Fatalf("reason bytes = %d valid=%v", len(reason), utf8.ValidString(reason))
	}

	transport := NewWaitForEnvironmentHandler(environmentWaiterFunc(func(context.Context, string) (EnvironmentStatus, string, error) {
		return EnvironmentStatusPending, "", errors.New("connection refused")
	}), []string{"env-1"}, nil)
	transport.pollInterval = time.Millisecond
	_, err = transport.Execute(context.Background(), invocation())
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("transport error reason = %v", err)
	}
}

func TestWaitForEnvironmentHandlerUsesBoundedHostDescriptionsLikeRust(t *testing.T) {
	custom := NewWaitForEnvironmentHandler(nil, nil, &WaitForEnvironmentToolConfig{
		ToolDescription:          "Host-provided wait tool description",
		EnvironmentIDDescription: "Host-provided environment ID description",
	})
	spec := custom.Spec()
	property := spec.InputSchema["properties"].(map[string]any)["environment_id"].(map[string]any)
	if spec.Description != "Host-provided wait tool description" || property["description"] != "Host-provided environment ID description" {
		t.Fatalf("custom spec = %#v", spec)
	}
	encoded, err := json.Marshal(spec)
	if err != nil || len(encoded) > maxWaitForEnvironmentSerializedToolSpecBytes {
		t.Fatalf("custom encoded bytes = %d, err = %v", len(encoded), err)
	}

	oversized := NewWaitForEnvironmentHandler(nil, nil, &WaitForEnvironmentToolConfig{
		ToolDescription:          strings.Repeat("x", maxWaitForEnvironmentCombinedDescriptionBytes),
		EnvironmentIDDescription: "overflow",
	})
	if oversized.Spec().Description != DefaultWaitForEnvironmentToolDescription {
		t.Fatalf("oversized description did not fall back")
	}
}
