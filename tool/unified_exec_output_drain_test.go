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
