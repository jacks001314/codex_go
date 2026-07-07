package runtimeutil

import (
	"context"
	"errors"
	"time"
)

var (
	ErrCancelled     = errors.New("cancelled")
	ErrChannelClosed = errors.New("channel closed before completion")
)

type AsyncResult[T any] struct {
	Value T
	Err   error
}

func OrCancel[T any](ctx context.Context, values <-chan T) (T, error) {
	var zero T
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return zero, ErrCancelled
	case value, ok := <-values:
		if !ok {
			return zero, ErrChannelClosed
		}
		return value, nil
	}
}

func ResultOrCancel[T any](ctx context.Context, results <-chan AsyncResult[T]) (T, error) {
	var zero T
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return zero, ErrCancelled
	case result, ok := <-results:
		if !ok {
			return zero, ErrChannelClosed
		}
		if result.Err != nil {
			return zero, result.Err
		}
		return result.Value, nil
	}
}

func SleepOrCancel(ctx context.Context, duration time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ErrCancelled
	case <-timer.C:
		return nil
	}
}
