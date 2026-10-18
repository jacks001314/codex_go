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
	tasks     chan func() error
	closed    chan struct{}
	done      chan struct{}
	busy      atomic.Bool
	closeOnce sync.Once
	mu        sync.Mutex
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
		case <-w.closed:
			return
		}
	}
}

func (w *clipboardWorker) submit(task func() error) (<-chan error, error) {
	if w == nil {
		return nil, errors.New("clipboard worker stopped")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	select {
	case <-w.closed:
		return nil, errors.New("clipboard worker stopped")
	default:
	}
	if !w.busy.CompareAndSwap(false, true) {
		return nil, errors.New("clipboard busy")
	}
	result := make(chan error, 1)
	w.tasks <- func() error {
		err := task()
		result <- err
		w.busy.Store(false)
		return nil
	}
	return result, nil
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
