package tea

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

// Rust parity: codex-rs/tui/src/bottom_pane/mention_usage.rs (#55596).
// The composer remembers a bounded window of confirmed `$` selections, ranks
// matches by that frequency within each match tier, and persists the window in
// CODEX_HOME/mention_usage.json when history persistence is enabled.

const (
	mentionUsageHistoryLimit  = 50
	mentionUsageMaxFileBytes  = 1 << 20
	mentionUsageFileName      = "mention_usage.json"
	mentionUsageLockTimeout   = 100 * time.Millisecond
	mentionUsageLockRetry     = 10 * time.Millisecond
	mentionUsageFlushTimeout  = time.Second
	mentionUsageUpdateBacklog = 50
)

type mentionUsage struct {
	mu      sync.Mutex
	recent  []string
	path    string
	persist bool
	closed  bool
	updates chan string
	done    chan struct{}
}

func newMentionUsage(codexHome string, persistence string) *mentionUsage {
	usage := &mentionUsage{}
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" || strings.EqualFold(strings.TrimSpace(persistence), "none") {
		return usage
	}
	usage.path = filepath.Join(codexHome, mentionUsageFileName)
	usage.persist = true
	usage.recent = readMentionUsage(usage.path)
	usage.updates = make(chan string, mentionUsageUpdateBacklog)
	usage.done = make(chan struct{})
	go usage.runWriter()
	return usage
}

func (u *mentionUsage) runWriter() {
	defer close(u.done)
	for identity := range u.updates {
		_ = persistMentionSelection(u.path, identity)
	}
}

func (u *mentionUsage) frequencies() map[string]int {
	if u == nil {
		return nil
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.recent) == 0 {
		return nil
	}
	counts := make(map[string]int, len(u.recent))
	for _, identity := range u.recent {
		counts[identity]++
	}
	return counts
}

func (u *mentionUsage) record(identity string) {
	if u == nil {
		return
	}
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return
	}
	u.mu.Lock()
	if u.closed {
		u.mu.Unlock()
		return
	}
	u.recent = appendMentionUsageRecent(u.recent, identity)
	persist := u.persist
	updates := u.updates
	u.mu.Unlock()
	if !persist {
		return
	}
	select {
	case updates <- identity:
	default:
		// Rust drops an update when its bounded queue is full instead of
		// blocking the composer.
	}
}

// flush closes the writer queue and waits up to Rust's one-second budget.
func (u *mentionUsage) flush() {
	if u == nil || !u.persist {
		return
	}
	u.mu.Lock()
	if !u.closed {
		u.closed = true
		close(u.updates)
	}
	done := u.done
	u.mu.Unlock()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(mentionUsageFlushTimeout):
	}
}

func appendMentionUsageRecent(recent []string, identity string) []string {
	if len(recent) >= mentionUsageHistoryLimit {
		recent = append([]string(nil), recent[len(recent)-mentionUsageHistoryLimit+1:]...)
	}
	return append(recent, identity)
}

func readMentionUsage(path string) []string {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if info.Size() > mentionUsageMaxFileBytes {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var recent []string
	if err := json.Unmarshal(data, &recent); err != nil {
		return nil
	}
	if len(recent) > mentionUsageHistoryLimit {
		recent = append([]string(nil), recent[len(recent)-mentionUsageHistoryLimit:]...)
	}
	return recent
}

func persistMentionSelection(path string, identity string) error {
	lock := flock.New(strings.TrimSuffix(path, filepath.Ext(path)) + ".lock")
	ctx, cancel := context.WithTimeout(context.Background(), mentionUsageLockTimeout)
	defer cancel()
	locked, err := lock.TryLockContext(ctx, mentionUsageLockRetry)
	if err != nil {
		return err
	}
	if !locked {
		return context.DeadlineExceeded
	}
	defer func() { _ = lock.Unlock() }()

	recent := appendMentionUsageRecent(readMentionUsage(path), identity)
	contents, err := json.Marshal(recent)
	if err != nil {
		return err
	}
	if len(contents) > mentionUsageMaxFileBytes {
		return os.ErrInvalid
	}
	return writeMentionUsageAtomic(path, contents)
}

func writeMentionUsageAtomic(path string, contents []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".mention-usage-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if _, err := temp.Write(contents); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}
