package session

import (
	"fmt"
	"sync"
)

// LiveThread is the persistence handle for one loaded thread. It serializes
// shutdown against store operations and owns the optional cross-process writer
// lock for histories that require a single writer.
type LiveThread struct {
	mu          sync.RWMutex
	store       *Store
	threadID    ThreadID
	historyMode string
	writer      *WriterLock
	closed      bool
}

// liveThreadRegistry makes a process-owned writer visible to every Router
// serving the same session root. The daemon creates a Router per connection;
// without this registry a fork on a new connection mistakes the live writer
// owned by another connection for a cross-process writer conflict.
var liveThreadRegistry = struct {
	sync.RWMutex
	byKey map[string]*LiveThread
}{byKey: map[string]*LiveThread{}}

func liveThreadKey(store *Store, threadID ThreadID) string {
	if store == nil {
		return ""
	}
	return storeRootKey(store.Root()) + "\x00" + string(threadID)
}

func registerLiveThread(liveThread *LiveThread) {
	if liveThread == nil {
		return
	}
	key := liveThreadKey(liveThread.store, liveThread.threadID)
	if key == "" {
		return
	}
	liveThreadRegistry.Lock()
	liveThreadRegistry.byKey[key] = liveThread
	liveThreadRegistry.Unlock()
}

func unregisterLiveThread(liveThread *LiveThread) {
	if liveThread == nil {
		return
	}
	key := liveThreadKey(liveThread.store, liveThread.threadID)
	liveThreadRegistry.Lock()
	if liveThreadRegistry.byKey[key] == liveThread {
		delete(liveThreadRegistry.byKey, key)
	}
	liveThreadRegistry.Unlock()
}

// LiveThread returns the process-owned live persistence handle for threadID
// under this store root. A nil result means the thread is not loaded by this
// process; another process may still own the on-disk writer.
func (s *Store) LiveThread(threadID ThreadID) *LiveThread {
	if s == nil {
		return nil
	}
	liveThreadRegistry.RLock()
	liveThread := liveThreadRegistry.byKey[liveThreadKey(s, threadID)]
	liveThreadRegistry.RUnlock()
	return liveThread
}

// LiveThreadInitGuard owns a newly opened handle until initialization commits.
// Callers should defer Discard and take ownership with Commit after all other
// initialization steps have succeeded.
type LiveThreadInitGuard struct {
	liveThread *LiveThread
}

func OpenLiveThread(store *Store, record *Record, retainWriter bool) (*LiveThreadInitGuard, error) {
	if store == nil {
		return nil, fmt.Errorf("thread store is nil")
	}
	if record == nil {
		return nil, fmt.Errorf("%w: record is nil", ErrInvalidThreadID)
	}
	if err := validateThreadID(record.ID); err != nil {
		return nil, err
	}
	var writer *WriterLock
	var err error
	if retainWriter {
		writer, err = store.AcquireWriter(record.ID)
		if err != nil {
			return nil, err
		}
	}
	return &LiveThreadInitGuard{liveThread: &LiveThread{
		store:       store,
		threadID:    record.ID,
		historyMode: record.Metadata.HistoryMode,
		writer:      writer,
	}}, nil
}

func (g *LiveThreadInitGuard) LiveThread() *LiveThread {
	if g == nil {
		return nil
	}
	return g.liveThread
}

func (g *LiveThreadInitGuard) Commit() *LiveThread {
	if g == nil {
		return nil
	}
	liveThread := g.liveThread
	g.liveThread = nil
	registerLiveThread(liveThread)
	return liveThread
}

func (g *LiveThreadInitGuard) Discard() error {
	if g == nil || g.liveThread == nil {
		return nil
	}
	liveThread := g.liveThread
	g.liveThread = nil
	return liveThread.Close()
}

func (l *LiveThread) ThreadID() ThreadID {
	if l == nil {
		return ""
	}
	return l.threadID
}

func (l *LiveThread) HistoryMode() string {
	if l == nil {
		return ""
	}
	return l.historyMode
}

func (l *LiveThread) OwnsWriter() bool {
	if l == nil {
		return false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return !l.closed && l.writer != nil
}

func (l *LiveThread) Read(includeArchived bool, includeHistory bool) (*Record, error) {
	if l == nil {
		return nil, fmt.Errorf("%w: live thread is nil", ErrInvalidThreadID)
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.closed {
		return nil, l.closedError()
	}
	return l.store.Read(l.threadID, includeArchived, includeHistory)
}

func (l *LiveThread) Save(record *Record) error {
	if l == nil {
		return fmt.Errorf("%w: live thread is nil", ErrInvalidThreadID)
	}
	if record == nil || record.ID != l.threadID {
		return fmt.Errorf("%w: live thread %s cannot save record %v", ErrInvalidThreadID, l.threadID, recordThreadID(record))
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.closed {
		return l.closedError()
	}
	return l.store.Save(record)
}

func (l *LiveThread) AppendItems(items []Item) (*Record, error) {
	if l == nil {
		return nil, fmt.Errorf("%w: live thread is nil", ErrInvalidThreadID)
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.closed {
		return nil, l.closedError()
	}
	if len(items) == 0 {
		return l.store.Read(l.threadID, true, true)
	}
	return l.store.AppendItems(l.threadID, items)
}

func (l *LiveThread) UpdateMetadata(patch *MetadataPatch, includeArchived bool) (*Record, error) {
	if l == nil {
		return nil, fmt.Errorf("%w: live thread is nil", ErrInvalidThreadID)
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.closed {
		return nil, l.closedError()
	}
	return l.store.UpdateMetadata(l.threadID, patch, includeArchived)
}

func (l *LiveThread) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	writer := l.writer
	l.writer = nil
	l.mu.Unlock()
	var closeErr error
	if writer != nil {
		closeErr = writer.Close()
	}
	unregisterLiveThread(l)
	return closeErr
}

func (l *LiveThread) closedError() error {
	return fmt.Errorf("%w: live thread %s is closed", ErrConflict, l.threadID)
}

func recordThreadID(record *Record) ThreadID {
	if record == nil {
		return ""
	}
	return record.ID
}
