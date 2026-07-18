package runtimeutil

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOrCancelReturnsValue(t *testing.T) {
	values := make(chan int, 1)
	values <- 42
	got, err := OrCancel(context.Background(), values)
	if err != nil || got != 42 {
		t.Fatalf("OrCancel() = %d, %v", got, err)
	}
}

func TestOrCancelReturnsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := OrCancel[int](ctx, make(chan int)); !errors.Is(err, ErrCancelled) {
		t.Fatalf("expected cancelled, got %v", err)
	}
}

func TestResultOrCancelReturnsInnerError(t *testing.T) {
	results := make(chan AsyncResult[int], 1)
	inner := errors.New("boom")
	results <- AsyncResult[int]{Err: inner}
	if _, err := ResultOrCancel(context.Background(), results); !errors.Is(err, inner) {
		t.Fatalf("expected inner error, got %v", err)
	}
}

func TestSleepOrCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := SleepOrCancel(ctx, time.Second); !errors.Is(err, ErrCancelled) {
		t.Fatalf("expected cancelled sleep, got %v", err)
	}
}
