package appserver

import (
	"context"
	"fmt"
	"time"
)

const appServerClockPollInterval = time.Second

type appServerClockProvider struct {
	router *RuntimeRouter
}

func (p *appServerClockProvider) CurrentTime(ctx context.Context, threadID string) (time.Time, error) {
	if p == nil || p.router == nil {
		return time.Time{}, fmt.Errorf("%w: app-server current-time provider is unavailable", ErrInvalidRequest)
	}
	return p.router.requestCurrentTime(ctx, threadID)
}

func (p *appServerClockProvider) Sleep(ctx context.Context, threadID string, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	startedAt, err := p.CurrentTime(ctx, threadID)
	if err != nil {
		return err
	}
	wakeAt := startedAt.Add(duration)
	ticker := time.NewTicker(appServerClockPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			current, err := p.CurrentTime(ctx, threadID)
			if err != nil {
				return err
			}
			if !current.Before(wakeAt) {
				return nil
			}
		}
	}
}
