package tea

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// clipboardWorker owns one blocking clipboard operation at a time for the
// session. Abandoning a setup on timeout does not interrupt the OS call; the
// worker remains busy until it returns (Rust #47847).
type clipboardWorker struct {
	tasks         chan func() error
	closed        chan struct{}
	done          chan struct{}
	busy          atomic.Bool
	closeOnce     sync.Once
	mu            sync.Mutex
	primaryText   string
	primaryWriter func(string) error
	primaryResult chan error
}

func newClipboardWorker() *clipboardWorker {
	worker := &clipboardWorker{
		tasks:  make(chan func() error),
		closed: make(chan struct{}),
		done:   make(chan struct{}),
	}
	go worker.run()
	return worker
}

func (w *clipboardWorker) run() {
	defer close(w.done)
	for {
		select {
		case task, ok := <-w.tasks:
			if !ok {
				return
			}
			task()
			w.drainPrimaryAndRelease()
		case <-w.closed:
			return
		}
	}
}

func (w *clipboardWorker) queuePrimary(text string, writer func(string) error) (<-chan error, error) {
	if w == nil || writer == nil {
		return nil, errors.New("primary selection unavailable")
	}
	w.mu.Lock()
	select {
	case <-w.closed:
		w.mu.Unlock()
		return nil, errors.New("clipboard worker stopped")
	default:
	}
	if w.primaryResult != nil {
		// Only the latest selection publication survives a busy worker.
		w.primaryResult <- errors.New("primary publication superseded")
		close(w.primaryResult)
	}
	result := make(chan error, 1)
	w.primaryText = text
	w.primaryWriter = writer
	w.primaryResult = result
	needTask := w.busy.CompareAndSwap(false, true)
	w.mu.Unlock()
	if needTask {
		select {
		case w.tasks <- func() error { return nil }:
		case <-w.closed:
			w.busy.Store(false)
			return nil, errors.New("clipboard worker stopped")
		}
	}
	return result, nil
}

func (w *clipboardWorker) flushPendingPrimary() {
	w.mu.Lock()
	text := w.primaryText
	writer := w.primaryWriter
	result := w.primaryResult
	w.primaryText = ""
	w.primaryWriter = nil
	w.primaryResult = nil
	w.mu.Unlock()
	if writer == nil {
		return
	}
	err := writer(text)
	if result != nil {
		result <- err
		close(result)
	}
}

func (w *clipboardWorker) drainPrimaryAndRelease() {
	for {
		w.flushPendingPrimary()
		w.mu.Lock()
		if w.primaryWriter == nil {
			w.busy.Store(false)
			w.mu.Unlock()
			return
		}
		w.mu.Unlock()
	}
}

func (w *clipboardWorker) submit(task func() error) (<-chan error, error) {
	if w == nil {
		return nil, errors.New("clipboard worker stopped")
	}
	w.mu.Lock()
	select {
	case <-w.closed:
		w.mu.Unlock()
		return nil, errors.New("clipboard worker stopped")
	default:
	}
	if !w.busy.CompareAndSwap(false, true) {
		w.mu.Unlock()
		return nil, errors.New("clipboard busy")
	}
	result := make(chan error, 1)
	taskFn := func() error {
		err := task()
		result <- err
		return nil
	}
	w.mu.Unlock()
	select {
	case w.tasks <- taskFn:
		return result, nil
	case <-w.closed:
		w.busy.Store(false)
		return nil, errors.New("clipboard worker stopped")
	}
}

func (w *clipboardWorker) isBusy() bool {
	return w != nil && w.busy.Load()
}

// close stops accepting work and waits up to timeout for the active OS call to
// return. It reports whether the worker exited within the bound.
func (w *clipboardWorker) close(timeout time.Duration) bool {
	if w == nil {
		return true
	}
	w.mu.Lock()
	w.closeOnce.Do(func() { close(w.closed) })
	w.mu.Unlock()
	if timeout <= 0 {
		timeout = time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-w.done:
		return true
	case <-timer.C:
		return false
	}
}
