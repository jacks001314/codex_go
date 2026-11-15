package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"codex_go/config"
)

type fakeCloudNameTransport struct {
	readName      string
	readSupported bool
	saved         []string
	saveSupported bool
}

func (f *fakeCloudNameTransport) ReadThreadName(context.Context, string) (string, bool, error) {
	return f.readName, f.readSupported, nil
}

func (f *fakeCloudNameTransport) SaveThreadName(_ context.Context, threadID string, name string) (bool, error) {
	if !f.saveSupported {
		return false, nil
	}
	f.saved = append(f.saved, threadID+":"+name)
	return true, nil
}

// Mirrors Rust #61600 AutomaticThreadName::save: the generated title is saved
// only while the conversation still has the expected (empty) name.
func TestSaveAutomaticCloudThreadNameLikeRust(t *testing.T) {
	ctx := context.Background()
	transport := &fakeCloudNameTransport{readSupported: true, saveSupported: true}
	if err := saveAutomaticCloudThreadName(ctx, transport, "thread-1", "Generated", ""); err != nil {
		t.Fatalf("save: %v", err)
	}
	if len(transport.saved) != 1 || transport.saved[0] != "thread-1:Generated" {
		t.Fatalf("saved = %#v", transport.saved)
	}

	renamed := &fakeCloudNameTransport{readName: "Manual", readSupported: true, saveSupported: true}
	if err := saveAutomaticCloudThreadName(ctx, renamed, "thread-1", "Generated", ""); err != nil {
		t.Fatalf("preserve save: %v", err)
	}
	if len(renamed.saved) != 0 {
		t.Fatalf("manual rename was overwritten: %#v", renamed.saved)
	}

	// Older/native-less clients fall back to the queued write path.
	unsupportedRead := &fakeCloudNameTransport{saveSupported: true}
	if err := saveAutomaticCloudThreadName(ctx, unsupportedRead, "thread-1", "Fallback", ""); err != nil {
		t.Fatalf("unsupported read save: %v", err)
	}
	if len(unsupportedRead.saved) != 1 {
		t.Fatalf("unsupported read did not save: %#v", unsupportedRead.saved)
	}
	unsupportedWrite := &fakeCloudNameTransport{readSupported: true}
	if err := saveAutomaticCloudThreadName(ctx, unsupportedWrite, "thread-1", "Title", ""); err == nil ||
		!strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("unsupported write error = %v", err)
	}
}

// Mirrors Rust #61600: a manual rename fails fast while an automatic Cloud
// write holds the thread, then succeeds once the write settles.
func TestAutomaticThreadNameGateLikeRust(t *testing.T) {
	gate := newAutomaticThreadNameGate()
	release := gate.acquire("thread-1")
	if _, ok := gate.tryAcquire("thread-1"); ok {
		t.Fatal("manual rename acquired a held thread")
	}
	if _, ok := gate.tryAcquire("thread-2"); !ok {
		t.Fatal("another thread was blocked by the held one")
	}
	release()
	manualRelease, ok := gate.tryAcquire("thread-1")
	if !ok {
		t.Fatal("manual rename did not acquire after the automatic write")
	}
	manualRelease()

	// acquire waits for an in-flight manual attempt instead of interleaving.
	manualRelease, ok = gate.tryAcquire("thread-1")
	if !ok {
		t.Fatal("manual rename did not acquire an idle thread")
	}
	acquired := make(chan struct{})
	go func() {
		gate.acquire("thread-1")
		close(acquired)
	}()
	select {
	case <-acquired:
		t.Fatal("automatic write did not wait for the manual rename")
	default:
	}
	manualRelease()
	<-acquired
}

// Mirrors Rust #61600: only an explicitly configured ChatGPT backend requires
// the discovered workspace origin to match.
func TestCloudChatGPTBaseURLIsExplicitLikeRust(t *testing.T) {
	if cloudChatGPTBaseURLIsExplicit(nil) {
		t.Fatal("nil config reported an explicit backend")
	}
	defaultConfig := &config.Config{Values: map[string]any{}}
	if cloudChatGPTBaseURLIsExplicit(defaultConfig) {
		t.Fatal("default backend reported explicit")
	}
	custom := &config.Config{Values: map[string]any{"chatgpt_base_url": "https://chatgpt-staging.com/backend-api"}}
	if !cloudChatGPTBaseURLIsExplicit(custom) {
		t.Fatal("configured backend not detected")
	}
}

var errCloudSaveFixture = errors.New("save failed")
