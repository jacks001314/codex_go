package tool

import (
	"io"
	"sync"
	"testing"
	"time"
)

type blockingUnifiedExecReadCloser struct {
	done chan struct{}
	once sync.Once
}

func newBlockingUnifiedExecReadCloser() *blockingUnifiedExecReadCloser {
	return &blockingUnifiedExecReadCloser{done: make(chan struct{})}
}

func (c *blockingUnifiedExecReadCloser) Read([]byte) (int, error) {
	<-c.done
	return 0, nil
}

func (c *blockingUnifiedExecReadCloser) Close() error {
	c.once.Do(func() { close(c.done) })
	return nil
}

func TestLocalUnifiedExecOutputDrainIsBoundedAfterExitLikeRust(t *testing.T) {
	reader := newBlockingUnifiedExecReadCloser()
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		_, _ = reader.Read(nil)
	}()

	timedOut := waitForUnifiedExecOutputDrain(&readers, []io.ReadCloser{reader}, 10*time.Millisecond)
	if !timedOut {
		t.Fatal("inherited output reader was not bounded after process exit")
	}

	// A reader that closes normally does not consume the grace deadline.
	fast := newBlockingUnifiedExecReadCloser()
	var fastReaders sync.WaitGroup
	fastReaders.Add(1)
	go func() {
		defer fastReaders.Done()
		_, _ = fast.Read(nil)
	}()
	_ = fast.Close()
	started := time.Now()
	if timedOut := waitForUnifiedExecOutputDrain(&fastReaders, []io.ReadCloser{fast}, time.Second); timedOut {
		t.Fatal("closed output reader was reported as timed out")
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("closed reader drain took %v", elapsed)
	}
}

// Rust #63664: output closure must wake a post-exit collector, and bytes
// appended immediately before closure must survive the final drain. Go waits
// for the reader goroutines before publishing process.done, so the same
// ordering is enforced without a separate cancellation token.
func TestLocalUnifiedExecOutputClosurePublishesFinalBytesLikeRust(t *testing.T) {
	reader, writer := io.Pipe()
	process := &unifiedExecProcess{
		done:       make(chan struct{}),
		eventDone:  make(chan struct{}),
		output:     newUnifiedExecHeadTailBuffer(unifiedExecOutputMaxBytes),
		transcript: newUnifiedExecHeadTailBuffer(unifiedExecOutputMaxBytes),
	}
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		defer reader.Close()
		_, _ = io.Copy(unifiedExecOutputWriter{process: process}, reader)
	}()

	drained := make(chan bool, 1)
	go func() {
		drained <- waitForUnifiedExecOutputDrain(&readers, []io.ReadCloser{reader}, time.Second)
	}()
	if _, err := writer.Write([]byte("final bytes")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case timedOut := <-drained:
		if timedOut {
			t.Fatal("output closure consumed the post-exit grace deadline")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("output closure did not wake the post-exit collector")
	}

	process.finish(nil)
	output, _, exited, _, _, _ := process.snapshotAndDrain()
	if !exited {
		t.Fatal("process was not finalized after its output reader drained")
	}
	if output != "final bytes" {
		t.Fatalf("final output = %q, want %q", output, "final bytes")
	}
}
