package windowssandbox

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

func TestInputForwarderSendsChunksAndReportsEOF(t *testing.T) {
	session := &recordingSession{}
	done := SpawnInputForwarder(bytes.NewReader([]byte("first\nsecond\n")), session)
	<-done
	if got := string(bytes.Join(session.stdinChunks, nil)); got != "first\nsecond\n" {
		t.Fatalf("stdin chunks = %q", got)
	}
	if !session.stdinClosed {
		t.Fatalf("stdin was not closed")
	}
}

func TestOutputForwarderWritesAllChunks(t *testing.T) {
	output := make(chan []byte, 2)
	var sink bytes.Buffer
	done := SpawnOutputForwarder(output, &sink)
	output <- []byte("alpha")
	output <- []byte("beta")
	close(output)
	<-done
	if sink.String() != "alphabeta" {
		t.Fatalf("output = %q", sink.String())
	}
}

func TestForwardSandboxSessionStdioReturnsExitCodeAndDrainsOutput(t *testing.T) {
	session := &recordingSession{}
	stdoutCh := make(chan []byte, 1)
	stderrCh := make(chan []byte, 1)
	exitCh := make(chan int, 1)
	stdoutCh <- []byte("out")
	stderrCh <- []byte("err")
	close(stdoutCh)
	close(stderrCh)
	exitCh <- 7
	close(exitCh)
	var stdout, stderr bytes.Buffer
	code := ForwardSandboxSessionStdio(SpawnedSandboxSession{
		Session: session,
		Stdout:  stdoutCh,
		Stderr:  stderrCh,
		Exit:    exitCh,
	}, bytes.NewReader([]byte("input")), &stdout, &stderr, nil)
	if code != 7 {
		t.Fatalf("exit code = %d", code)
	}
	if stdout.String() != "out" || stderr.String() != "err" {
		t.Fatalf("stdout/stderr = %q/%q", stdout.String(), stderr.String())
	}
	if got := string(bytes.Join(session.stdinChunks, nil)); got != "input" {
		t.Fatalf("stdin = %q", got)
	}
}

func TestForwardSandboxSessionStdioTerminatesOnInterrupt(t *testing.T) {
	session := &recordingSession{}
	exitCh := make(chan int)
	interrupt := make(chan struct{})
	go func() {
		close(interrupt)
		for {
			session.mu.Lock()
			terminated := session.terminated
			session.mu.Unlock()
			if terminated {
				break
			}
			time.Sleep(time.Millisecond)
		}
		exitCh <- -1
		close(exitCh)
	}()
	code := ForwardSandboxSessionStdio(SpawnedSandboxSession{
		Session: session,
		Stdout:  closedBytesChannel(),
		Stderr:  closedBytesChannel(),
		Exit:    exitCh,
	}, bytes.NewReader(nil), nil, nil, interrupt)
	if code != -1 {
		t.Fatalf("exit code = %d", code)
	}
	if !session.terminated {
		t.Fatalf("session was not terminated")
	}
}

type recordingSession struct {
	mu          sync.Mutex
	stdinChunks [][]byte
	stdinClosed bool
	terminated  bool
}

func (s *recordingSession) SendStdin(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stdinChunks = append(s.stdinChunks, append([]byte(nil), data...))
	return nil
}

func (s *recordingSession) CloseStdin() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stdinClosed = true
}

func (s *recordingSession) RequestTerminate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.terminated = true
}

func closedBytesChannel() <-chan []byte {
	ch := make(chan []byte)
	close(ch)
	return ch
}
