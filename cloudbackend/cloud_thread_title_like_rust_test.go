package cloudbackend

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/appserver"
)

type recordingTitleGenerator struct {
	mu     sync.Mutex
	model  string
	effort string
	prompt string
	schema map[string]any
	err    error
}

func (g *recordingTitleGenerator) Generate(_ context.Context, model string, effort string, prompt string, schema map[string]any) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.model, g.effort, g.prompt, g.schema = model, effort, prompt, schema
	if g.err != nil {
		return "", g.err
	}
	return `{"title":"Generated"}`, nil
}

// Mirrors Rust #61600: a saved name is read and written through the native
// client without occupying the conversation queue, and the write publishes
// thread/name/updated.
func TestCloudThreadNameReadWriteOutsideQueueLikeRust(t *testing.T) {
	var mu sync.Mutex
	writtenThread, writtenName := "", ""
	fake := &fakeCloud{
		readThreadName: func(_ context.Context, threadID string) (string, error) {
			return "Saved title", nil
		},
		setThreadName: func(_ context.Context, threadID string, name string) error {
			mu.Lock()
			writtenThread, writtenName = threadID, name
			mu.Unlock()
			return nil
		},
	}
	generator := &recordingTitleGenerator{}
	client := ConnectWithOptions(context.Background(), fake, nil, ConnectOptions{TitleGenerator: generator})
	t.Cleanup(client.Close)
	events := client.Notifications()

	name, supported, err := client.ReadThreadName(context.Background(), "thread-1")
	if !supported || err != nil || name != "Saved title" {
		t.Fatalf("ReadThreadName = %q, %v, supported=%v", name, err, supported)
	}
	supported, err = client.SaveThreadName(context.Background(), "thread-1", "  New title  ")
	if !supported || err != nil {
		t.Fatalf("SaveThreadName = supported=%v, %v", supported, err)
	}
	mu.Lock()
	gotThread, gotName := writtenThread, writtenName
	mu.Unlock()
	if gotThread != "thread-1" || gotName != "New title" {
		t.Fatalf("written name = %q for %q", gotName, gotThread)
	}
	select {
	case notification := <-events:
		payload, ok := notification.Params.(*appserver.ThreadNameUpdatedNotification)
		if notification.Method != appserver.NotificationThreadNameUpdated || !ok ||
			payload.ThreadID != "thread-1" || payload.ThreadName == nil || *payload.ThreadName != "New title" {
			t.Fatalf("notification = %#v", notification)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("name save did not publish a notification")
	}

	title, err := client.GenerateThreadTitle(context.Background(), "title-model", "low", "Fix login", map[string]any{"type": "object"})
	if err != nil || title != `{"title":"Generated"}` {
		t.Fatalf("GenerateThreadTitle = %q, %v", title, err)
	}
	generator.mu.Lock()
	defer generator.mu.Unlock()
	if generator.model != "title-model" || generator.effort != "low" || generator.prompt != "Fix login" {
		t.Fatalf("generator call = %#v", generator)
	}
}

// Mirrors Rust #61600: without a title client generation is unavailable, and
// the TUI keeps the first-message title.
func TestCloudThreadTitleUnavailableLikeRust(t *testing.T) {
	client := Connect(context.Background(), &fakeCloud{}, nil)
	t.Cleanup(client.Close)
	if client.TitleGenerationAvailable() {
		t.Fatal("title generation reported available without a generator")
	}
	if _, err := client.GenerateThreadTitle(context.Background(), "m", "", "p", nil); err == nil ||
		!strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("GenerateThreadTitle error = %v", err)
	}
}

// Mirrors Rust #61600 thread_name_saved: the update and its notification are
// applied together on the dispatch goroutine.
func TestApplyThreadNameSavedLikeRust(t *testing.T) {
	name := "Old"
	client := newTestClient(&fakeCloud{})
	client.loaded = &appserver.ThreadResumeResponse{Thread: &appserver.Thread{ID: "thread-1", Name: &name}}
	client.applyThreadNameSaved(appserver.ThreadSetNameParams{ThreadID: "thread-1", Name: "New"})
	if client.loaded.Thread.Name == nil || *client.loaded.Thread.Name != "New" {
		t.Fatalf("loaded name = %v", client.loaded.Thread.Name)
	}
	notifications := client.testNotifications()
	if len(notifications) != 1 || notifications[0].Method != appserver.NotificationThreadNameUpdated {
		t.Fatalf("notifications = %#v", notifications)
	}
	// A save for another conversation must not overwrite the loaded one.
	client.applyThreadNameSaved(appserver.ThreadSetNameParams{ThreadID: "other", Name: "Elsewhere"})
	if *client.loaded.Thread.Name != "New" {
		t.Fatalf("loaded name changed by another conversation: %q", *client.loaded.Thread.Name)
	}
}

var errTitleGeneration = errors.New("generation failed")

// A generator failure is reported to the caller so the TUI can fall back.
func TestCloudThreadTitleGeneratorFailureLikeRust(t *testing.T) {
	client := ConnectWithOptions(context.Background(), &fakeCloud{}, nil, ConnectOptions{
		TitleGenerator: &recordingTitleGenerator{err: errTitleGeneration},
	})
	t.Cleanup(client.Close)
	if _, err := client.GenerateThreadTitle(context.Background(), "m", "", "p", nil); !errors.Is(err, errTitleGeneration) {
		t.Fatalf("GenerateThreadTitle error = %v", err)
	}
}
