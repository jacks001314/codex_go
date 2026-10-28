package appserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Rust #57569: older servers omit `outputDrainTimedOut`, so the field is
// optional in both directions.
func TestProcessExitedNotificationOutputDrainTimedOutShapeLikeRust(t *testing.T) {
	legacy := []byte(`{"processHandle":"h","exitCode":0,"stdout":"","stdoutCapReached":false,"stderr":"","stderrCapReached":false}`)
	var notification ProcessExitedNotification
	if err := json.Unmarshal(legacy, &notification); err != nil {
		t.Fatal(err)
	}
	if notification.OutputDrainTimedOut != nil {
		t.Fatalf("legacy payload set outputDrainTimedOut = %v", *notification.OutputDrainTimedOut)
	}

	encoded, err := json.Marshal(&ProcessExitedNotification{ProcessHandle: "h"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "outputDrainTimedOut") {
		t.Fatalf("absent field was serialized: %s", encoded)
	}

	value := false
	encoded, err = json.Marshal(&ProcessExitedNotification{ProcessHandle: "h", OutputDrainTimedOut: &value})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"outputDrainTimedOut":false`) {
		t.Fatalf("complete output was not reported: %s", encoded)
	}
}

func TestWaitForPTYOutputDoneReportsDrainDeadlineLikeRust(t *testing.T) {
	original := ptyOutputDrainTimeout
	ptyOutputDrainTimeout = 10 * time.Millisecond
	defer func() { ptyOutputDrainTimeout = original }()

	done := make(chan struct{})
	go func() {
		time.Sleep(30 * time.Millisecond)
		close(done)
	}()
	if !waitForPTYOutputDone(nil, done) {
		t.Fatal("expired drain deadline was not reported")
	}

	closed := make(chan struct{})
	close(closed)
	if waitForPTYOutputDone(nil, closed) {
		t.Fatal("closed stream reported a drain timeout")
	}
}

// Rust #57569 treats a fully captured requested byte cap as complete even when
// the post-exit deadline expired.
func TestProcessStreamOutputIncompleteRespectsCapturedCapLikeRust(t *testing.T) {
	limit := 5
	captured := newCommandExecOutputBuffer(&limit)
	_, _, _, _ = captured.WriteAndAccepted([]byte("abcde"))
	if processStreamOutputIncomplete(true, captured) {
		t.Fatal("fully captured cap reported incomplete output")
	}
	if !captured.CapSatisfied() {
		t.Fatal("CapSatisfied() = false for a full capture")
	}

	short := newCommandExecOutputBuffer(&limit)
	_, _, _, _ = short.WriteAndAccepted([]byte("ab"))
	if !processStreamOutputIncomplete(true, short) {
		t.Fatal("short capture reported complete output")
	}
	if processStreamOutputIncomplete(false, short) {
		t.Fatal("no drain timeout reported incomplete output")
	}

	uncapped := newCommandExecOutputBuffer(nil)
	_, _, _, _ = uncapped.WriteAndAccepted([]byte("abcde"))
	if !processStreamOutputIncomplete(true, uncapped) {
		t.Fatal("uncapped stream reported complete output after a drain timeout")
	}
}
