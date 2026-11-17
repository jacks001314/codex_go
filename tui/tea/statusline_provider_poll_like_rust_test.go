package tea

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	bubbletea "charm.land/bubbletea/v2"
)

const statuslinePollTestURI = "ui://fixture/line"

func statuslineProviderContents(text string) []StatuslineProviderContent {
	return []StatuslineProviderContent{{
		URI:      statuslinePollTestURI,
		MIMEType: "application/json",
		Text:     text,
		IsText:   true,
	}}
}

// compressStatuslineProviderClock stands in for the paused tokio clock Rust's
// statusline_reader_tests.rs uses, so the one-second cadence and deadline are
// exercised in milliseconds.
func compressStatuslineProviderClock(t *testing.T, poll, deadline time.Duration) {
	t.Helper()
	pollBefore, deadlineBefore := statuslineProviderPollInterval, statuslineProviderReadDeadline
	statuslineProviderPollInterval, statuslineProviderReadDeadline = poll, deadline
	t.Cleanup(func() {
		statuslineProviderPollInterval, statuslineProviderReadDeadline = pollBefore, deadlineBefore
	})
}

func statuslineProviderPollingModel(
	t *testing.T,
	read func(context.Context, StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error),
) *Model {
	t.Helper()
	return statuslineProviderFooterModel(t,
		func(context.Context, StatuslineProviderReadRequest) (StatuslineProvider, error) {
			return StatuslineProvider{Found: true, Title: "Fixture Line", URI: statuslinePollTestURI}, nil
		}, read)
}

func statuslineProviderFailureCopy() string {
	return "Error loading statusline contents from Fixture Line. Use /statusline to (re)select your provider and try again."
}

func statuslineSnapshotDisplayMode(t *testing.T, snapshot map[string]any) any {
	t.Helper()
	app, _ := snapshot["app"].(map[string]any)
	return app["display_mode"]
}

// Rust #61981 statusline_polls_one_second_after_completion_including_empty_content:
// the next read starts after the poll interval, carries the state the thread has
// at that moment, and empty content keeps polling.
func TestStatuslineProviderPollsWithFreshStateLikeRust(t *testing.T) {
	compressStatuslineProviderClock(t, 5*time.Millisecond, time.Second)
	var snapshots []map[string]any
	model := statuslineProviderPollingModel(t,
		func(_ context.Context, request StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error) {
			snapshots = append(snapshots, request.Snapshot)
			if len(snapshots) == 1 {
				return statuslineProviderContents(`"ready"`), nil
			}
			return statuslineProviderContents("null"), nil
		})
	model.refreshStatusControls()
	if got := model.statusLineContent(); got != statuslineProviderInitializing {
		t.Fatalf("initializing footer = %q", got)
	}

	poll := runProviderCmd(model, runProviderCmd(model, model.ensureProviderFooterCmd()))
	if got := model.statusLineContent(); got != "ready" {
		t.Fatalf("provider footer = %q", got)
	}
	if poll == nil {
		t.Fatal("successful read did not schedule the next poll")
	}

	// The thread changes while the poll delay is in flight; the next read must
	// capture that new state instead of reusing the first snapshot.
	model.rawOutput = true
	readCmd := runProviderCmd(model, poll)
	if readCmd == nil {
		t.Fatal("the poll did not start the next read")
	}
	next := runProviderCmd(model, readCmd)
	if len(snapshots) != 2 {
		t.Fatalf("reads = %d, want 2", len(snapshots))
	}
	if mode := statuslineSnapshotDisplayMode(t, snapshots[0]); mode != "standard" {
		t.Fatalf("first snapshot display mode = %#v", mode)
	}
	if mode := statuslineSnapshotDisplayMode(t, snapshots[1]); mode != "raw" {
		t.Fatalf("polled snapshot display mode = %#v", mode)
	}
	if got := model.statusLineContent(); got != "" {
		t.Fatalf("empty tree painted %q", got)
	}
	if next == nil {
		t.Fatal("empty content stopped polling")
	}
}

// Rust #61981 statusline_invalid_content_stops_polling_immediately.
func TestStatuslineProviderInvalidContentStopsPollingLikeRust(t *testing.T) {
	compressStatuslineProviderClock(t, 5*time.Millisecond, time.Second)
	reads := 0
	model := statuslineProviderPollingModel(t,
		func(context.Context, StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error) {
			reads++
			if reads == 1 {
				return statuslineProviderContents(`"last good"`), nil
			}
			return statuslineProviderContents("{"), nil
		})
	model.refreshStatusControls()
	poll := runProviderCmd(model, runProviderCmd(model, model.ensureProviderFooterCmd()))
	if got := model.statusLineContent(); got != "last good" {
		t.Fatalf("provider footer = %q", got)
	}
	next := runProviderCmd(model, runProviderCmd(model, poll))
	if next != nil {
		t.Fatal("invalid content kept polling")
	}
	if got := model.statusLineContent(); got != statuslineProviderFailureCopy() {
		t.Fatalf("failure copy = %q", got)
	}
	model.refreshStatusControls()
	if cmd := model.ensureProviderFooterCmd(); cmd != nil {
		t.Fatal("refresh restarted the failed read")
	}
	if reads != 2 {
		t.Fatalf("reads = %d, want 2", reads)
	}
}

// Rust #61981 statusline_rpc_error_stops_polling_immediately.
func TestStatuslineProviderRPCErrorStopsPollingLikeRust(t *testing.T) {
	compressStatuslineProviderClock(t, 5*time.Millisecond, time.Second)
	model := statuslineProviderPollingModel(t,
		func(context.Context, StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error) {
			return nil, errors.New("provider disconnected")
		})
	model.refreshStatusControls()
	next := runProviderCmd(model, runProviderCmd(model, model.ensureProviderFooterCmd()))
	if next != nil {
		t.Fatal("RPC error kept polling")
	}
	if got := model.statusLineContent(); got != statuslineProviderFailureCopy() {
		t.Fatalf("failure copy = %q", got)
	}
}

// Rust #61981 statusline_returning_to_a_thread_fetches_fresh_state_and_discards_its_old_reply:
// retiring the reader cancels the outstanding context and returning to the
// thread stages a brand new read.
func TestStatuslineProviderRetireCancelsOutstandingReadsLikeRust(t *testing.T) {
	compressStatuslineProviderClock(t, 5*time.Millisecond, time.Second)
	model := statuslineProviderPollingModel(t,
		func(ctx context.Context, _ StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})
	model.refreshStatusControls()
	read := model.statuslineProviderRead
	if read == nil {
		t.Fatal("provider read was not staged")
	}
	readCmd := model.statuslineProviderReadCmd(read)
	if readCmd == nil {
		t.Fatal("read command was not produced")
	}

	model.State.SetThreadID("thread-other")
	model.refreshStatusControls()
	select {
	case <-read.ctx.Done():
	default:
		t.Fatal("retiring the reader did not cancel its context")
	}
	msg, ok := readCmd().(statuslineProviderReadMsg)
	if !ok || msg.err == nil {
		t.Fatalf("cancelled read = %#v, want an error", msg)
	}
	model.applyStatuslineProviderRead(msg)
	if got := model.statusLineContent(); got != statuslineProviderInitializing {
		t.Fatalf("cancelled reply painted %q", got)
	}

	model.State.SetThreadID("thread-provider")
	model.refreshStatusControls()
	if model.statuslineProviderRead == nil || model.statuslineProviderRead.token == read.token {
		t.Fatal("returning to the thread did not stage a fresh read")
	}
}

// Rust #61981 statusline_deadline_wins_when_a_late_reply_is_already_queued.
func TestStatuslineProviderLateReplyAfterDeadlineFailsLikeRust(t *testing.T) {
	compressStatuslineProviderClock(t, 5*time.Millisecond, 5*time.Millisecond)
	model := statuslineProviderPollingModel(t,
		func(context.Context, StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error) {
			time.Sleep(30 * time.Millisecond)
			return statuslineProviderContents(`"too late"`), nil
		})
	model.refreshStatusControls()
	next := runProviderCmd(model, runProviderCmd(model, model.ensureProviderFooterCmd()))
	if next != nil {
		t.Fatal("late reply kept polling")
	}
	if got := model.statusLineContent(); got != statuslineProviderFailureCopy() {
		t.Fatalf("late reply painted %q", got)
	}
}

// Rust #61981 statusline_hung_read_stops_polling_after_one_second.
func TestStatuslineProviderHungReadStopsAtDeadlineLikeRust(t *testing.T) {
	compressStatuslineProviderClock(t, 5*time.Millisecond, 20*time.Millisecond)
	model := statuslineProviderPollingModel(t,
		func(ctx context.Context, _ StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})
	model.refreshStatusControls()
	started := time.Now()
	next := runProviderCmd(model, runProviderCmd(model, model.ensureProviderFooterCmd()))
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond {
		t.Fatalf("hung read returned after %v, before its deadline", elapsed)
	}
	if next != nil {
		t.Fatal("hung read kept polling")
	}
	if got := model.statusLineContent(); got != statuslineProviderFailureCopy() {
		t.Fatalf("hung read painted %q", got)
	}
}

// Rust #61981 statusline_startup_timeout_stops_before_the_first_read: the
// lookup runs under a five-second deadline that the reader's cancellation can
// end early.
func TestStatuslineProviderLookupDeadlineAndCancelLikeRust(t *testing.T) {
	compressStatuslineProviderClock(t, 5*time.Millisecond, time.Second)
	var deadlineOK bool
	var remaining time.Duration
	model := statuslineProviderFooterModel(t,
		func(ctx context.Context, _ StatuslineProviderReadRequest) (StatuslineProvider, error) {
			deadline, ok := ctx.Deadline()
			deadlineOK = ok
			if ok {
				remaining = time.Until(deadline)
			}
			<-ctx.Done()
			return StatuslineProvider{}, ctx.Err()
		},
		func(context.Context, StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error) {
			return nil, errors.New("read before resolve")
		})
	model.refreshStatusControls()
	cmd := model.ensureProviderFooterCmd()
	if cmd == nil {
		t.Fatal("startup lookup was not started")
	}
	done := make(chan bubbletea.Msg, 1)
	go func() { done <- cmd() }()
	model.retireStatuslineProviderRead()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled startup lookup did not return")
	}
	if !deadlineOK {
		t.Fatal("startup lookup ran without a deadline")
	}
	if remaining <= 0 || remaining > statuslineProviderLookupTimeout {
		t.Fatalf("startup deadline = %v", remaining)
	}
	if got := model.statusLineContent(); strings.Contains(got, "Error loading statusline contents") {
		t.Fatalf("retired lookup painted the footer: %q", got)
	}
}
