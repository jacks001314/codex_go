package appserverdaemon

import (
	"errors"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"codex_go/utils"
)

const (
	maxPreviousStderrLogBytes = 256 * 1024
	// maxCurrentStderrLogBytes bounds a managed stderr log between prunes
	// (Rust MAX_CURRENT_LOG_BYTES, #55856).
	maxCurrentStderrLogBytes = 1024 * 1024
	// stderrLogQueuedRecords bounds the nonblocking queue (Rust QUEUED_RECORDS).
	stderrLogQueuedRecords = 64
	// maxStderrLogRecordBytes drops records that a single write cannot bound
	// (Rust MAX_RECORD_BYTES).
	maxStderrLogRecordBytes = 16 * 1024
)

// managedStderrLogWriter mirrors Rust's nonblocking stderr worker: writes never
// wait for disk, and a full queue or an oversized record is dropped.
type managedStderrLogWriter struct {
	path  string
	queue chan []byte
	// enableAppend is a test seam; production uses enableStderrAppendMode.
	enableAppend func() error
	stopOnce     sync.Once
	stopCh       chan struct{}
	done         chan struct{}
}

func newManagedStderrLogWriter(path string) *managedStderrLogWriter {
	return &managedStderrLogWriter{
		path:   path,
		queue:  make(chan []byte, stderrLogQueuedRecords),
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
}

// stop ends the worker and closes its file. Production keeps the writer for the
// process lifetime, so only tests call this.
func (w *managedStderrLogWriter) stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
	<-w.done
}

var (
	managedStderrLog atomic.Pointer[managedStderrLogWriter]
	// stderrPruningMu serializes pruning with the updater handoff, which takes
	// it for the whole successor launch so a stalled worker cannot wait on it.
	stderrPruningMu sync.Mutex
	// stderrPruningPaused keeps pruning off after a successful handoff, when
	// the successor owns retention (Rust PRUNING_ENABLED).
	stderrPruningPaused atomic.Bool
)

// StderrWriter returns the managed bounded writer when logging has been
// started, and the process stderr otherwise (Rust `stderr_writer`).
func StderrWriter() io.Writer {
	if writer := managedStderrLog.Load(); writer != nil {
		return writer
	}
	return os.Stderr
}

// MaintainStderrLog starts the bounded stderr writer for the managed daemon
// process that launched this one. The daemon passes its pid file through
// CODEX_UPDATER_PID_FILE for both the app-server and updater children.
func MaintainStderrLog() error {
	pidFile := strings.TrimSpace(os.Getenv(UpdaterPIDFileEnv))
	if pidFile == "" {
		return errors.New("managed daemon pid file is not configured")
	}
	StartStderrLog(StderrLogPathForPIDFile(pidFile))
	return nil
}

// StartStderrLog routes writes for path through the bounded worker. The first
// caller wins so one process keeps one worker and one queue (Rust OnceLock).
func StartStderrLog(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	writer := newManagedStderrLogWriter(path)
	if !managedStderrLog.CompareAndSwap(nil, writer) {
		return
	}
	go writer.run()
}

// InstallStderrLogging sends the standard and structured loggers through the
// managed writer. The app-server chains its SQLite log handler over this
// logger, so both the database and the stderr mirror stay bounded (Rust
// `with_writer(stderr_writer)`).
func InstallStderrLogging() {
	writer := StderrWriter()
	log.SetOutput(writer)
	slog.SetDefault(slog.New(slog.NewTextHandler(writer, nil)))
}

// Write enqueues one complete record, or drops it when the record is oversized
// or the queue is full. It always reports success so logging never blocks a
// lifecycle operation (Rust lossy NonBlocking writer).
func (w *managedStderrLogWriter) Write(p []byte) (int, error) {
	if len(p) > maxStderrLogRecordBytes {
		return len(p), nil
	}
	record := make([]byte, len(p))
	copy(record, p)
	select {
	case w.queue <- record:
	default:
	}
	return len(p), nil
}

func (w *managedStderrLogWriter) run() {
	// Append-only writes follow EOF after a truncation, including for handles
	// other processes inherited before the file was pruned.
	enableAppend := w.enableAppend
	if enableAppend == nil {
		enableAppend = enableStderrAppendMode
	}
	appending := enableAppend() == nil
	var file *os.File
	defer func() {
		if file != nil {
			_ = file.Close()
		}
		close(w.done)
	}()
	for {
		select {
		case <-w.stopCh:
			return
		case record := <-w.queue:
			if appending {
				pruneCurrentStderrLog(w.path)
			}
			if file == nil {
				opened, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					continue
				}
				file = opened
			}
			if _, err := file.Write(record); err != nil {
				_ = file.Close()
				file = nil
			}
		}
	}
}

// pruneCurrentStderrLog keeps a live managed log bounded: once it exceeds
// 1 MiB, the last 256 KiB is preserved as `.log.previous` and the current file
// is truncated in place so inherited handles stay connected (Rust `prune`).
func pruneCurrentStderrLog(path string) {
	if !stderrPruningMu.TryLock() {
		return
	}
	defer stderrPruningMu.Unlock()
	if stderrPruningPaused.Load() {
		return
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= maxCurrentStderrLogBytes {
		return
	}
	if err := preserveStderrLog(path); err != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return
	}
	_ = file.Truncate(0)
	_ = file.Close()
}

// pauseStderrPruningForHandoff excludes pruning from the successor launch and
// from any ownership rollback (Rust `replace_current_updater`). The returned
// callback re-enables pruning only when ownership was restored after a failed
// handoff; a successful handoff leaves retention to the successor.
func pauseStderrPruningForHandoff() (func(ownershipRestored bool), error) {
	if !stderrPruningMu.TryLock() {
		return nil, errors.New("stderr pruning is busy; retry updater handoff")
	}
	stderrPruningPaused.Store(true)
	return func(ownershipRestored bool) {
		if ownershipRestored {
			stderrPruningPaused.Store(false)
		}
		stderrPruningMu.Unlock()
	}, nil
}

// preserveStderrLog saves the last 256 KiB of a detached launch's stderr log
// before the next launch truncates it. Missing, empty, symlinked, or nonregular
// files are ignored; preservation failures never block launch.
func preserveStderrLog(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() || opened.Size() == 0 {
		return nil
	}
	offset := opened.Size() - maxPreviousStderrLogBytes
	if offset < 0 {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(file, maxPreviousStderrLogBytes))
	if err != nil || len(data) == 0 {
		return err
	}
	return utils.WriteAtomically(path+".previous", string(data))
}

func preserveStderrLogBestEffort(path string) {
	if err := preserveStderrLog(path); err != nil {
		slog.Warn("failed to preserve previous stderr log", "path", path, "error", err)
	}
}
