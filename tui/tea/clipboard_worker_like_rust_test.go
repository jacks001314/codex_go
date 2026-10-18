package tea

import (
	"strings"
	"testing"
	"time"

	codextui "codex_go/tui"
)

// Rust #47847: a blocked setup is abandoned after the five-second budget while
// the worker stays busy until the OS call returns.
func TestClipboardWorkerSetupTimeoutLikeRust(t *testing.T) {
	block := make(chan struct{})
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 24,
		OnClipboardWrite: func(string) error {
			<-block
			return nil
		},
	})
	model.clipboardSetupTimeout = 20 * time.Millisecond
	cmd := model.clipboardWriteCmd("slow", "text", false, false)
	msg, ok := cmd().(ClipboardWriteResultMsg)
	if !ok || msg.Err == nil || !strings.Contains(msg.Err.Error(), "clipboard setup timed out") {
		t.Fatalf("timeout message = %#v", msg)
	}
	model.applyClipboardWriteResult(msg)
	if model.clipboardWorker == nil || !model.clipboardWorker.isBusy() {
		t.Fatal("worker should remain busy after abandoning setup")
	}
	if _, ok := model.clipboardWriteCmd("second", "text", false, false)().(ClipboardBusyMsg); !ok {
		t.Fatal("second copy should be rejected while the OS call is blocked")
	}
	close(block)
	deadline := time.Now().Add(time.Second)
	for model.clipboardWorker.isBusy() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if model.clipboardWorker.isBusy() {
		t.Fatal("worker stayed busy after the OS call returned")
	}
	model.closeClipboardWorker()
}

func TestClipboardWorkerBoundedCloseLikeRust(t *testing.T) {
	worker := newClipboardWorker()
	block := make(chan struct{})
	if _, err := worker.submit(func() error {
		<-block
		return nil
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if worker.close(10 * time.Millisecond) {
		t.Fatal("close reported success while a blocking setup was active")
	}
	close(block)
	if !worker.close(time.Second) {
		t.Fatal("worker did not stop within the bounded close window")
	}
}

func TestClipboardWorkerRetainsLatestPrimaryPublicationLikeRust(t *testing.T) {
	worker := newClipboardWorker()
	block := make(chan struct{})
	copyResult, err := worker.submit(func() error {
		<-block
		return nil
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	writes := []string{}
	writer := func(text string) error {
		writes = append(writes, text)
		return nil
	}
	first, err := worker.queuePrimary("first", writer)
	if err != nil {
		t.Fatalf("queue first primary: %v", err)
	}
	second, err := worker.queuePrimary("second", writer)
	if err != nil {
		t.Fatalf("queue second primary: %v", err)
	}
	close(block)
	if err := <-copyResult; err != nil {
		t.Fatalf("copy result: %v", err)
	}
	if err := <-first; err == nil {
		t.Fatal("superseded primary publication unexpectedly succeeded")
	}
	if err := <-second; err != nil {
		t.Fatalf("latest primary publication: %v", err)
	}
	if len(writes) != 1 || writes[0] != "second" {
		t.Fatalf("primary writes = %#v, want only latest", writes)
	}
	worker.close(time.Second)
}
