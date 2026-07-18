package codemode

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"codex_go/tool"
)

func TestWaitHandler(t *testing.T) {
	cells := NewCellStore()
	now := time.Date(2026, 6, 29, 8, 0, 0, 0, time.UTC)
	cells.SetClock(func() time.Time { return now })
	if _, err := cells.Start("cell-a", "source"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	handler := NewWaitHandler(cells)
	output, err := handler.Execute(context.Background(), &tool.Invocation{Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"cell_id":"cell-a"}`}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output.Body, `"Status":"running"`) {
		t.Fatalf("output = %s", output.Body)
	}
	_, err = handler.Execute(context.Background(), &tool.Invocation{Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"cell_id":"missing"}`}})
	if !errors.Is(err, ErrCellNotFound) {
		t.Fatalf("expected ErrCellNotFound, got %v", err)
	}
}
