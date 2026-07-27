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
	return writer.Close()
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
