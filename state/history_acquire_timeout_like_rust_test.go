package state

import (
	"context"
	"testing"
	"time"
)

// Rust #63456 bounds a rollback-journal pool's acquire wait at 120 seconds;
// WAL pools keep the caller's context because database/sql has no acquire
// timeout of its own.
func TestRollbackJournalAcquireTimeoutLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name string
		nfs  bool
		want time.Duration
	}{
		{name: "network filesystem", nfs: true, want: sqliteRollbackJournalAcquireTimeout},
		{name: "local disk", nfs: false, want: 0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			config, err := sqliteConfigWithFilesystemProbe(home, func(string) (bool, error) {
				return testCase.nfs, nil
			})
			if err != nil {
				t.Fatalf("sqliteConfigWithFilesystemProbe: %v", err)
			}
			if got := config.AcquireTimeout(); got != testCase.want {
				t.Fatalf("AcquireTimeout() = %v, want %v", got, testCase.want)
			}
			ctx, cancel := config.AcquireContext(context.Background())
			defer cancel()
			deadline, ok := ctx.Deadline()
			if testCase.want == 0 {
				if ok {
					t.Fatalf("WAL context gained a deadline: %v", deadline)
				}
				return
			}
			if !ok {
				t.Fatal("rollback-journal context has no acquire deadline")
			}
			if remaining := time.Until(deadline); remaining <= 0 || remaining > testCase.want {
				t.Fatalf("acquire deadline remaining = %v, want at most %v", remaining, testCase.want)
			}
		})
	}
}

func TestAcquireContextKeepsCallerDeadlineLikeRust(t *testing.T) {
	home := t.TempDir()
	config, err := sqliteConfigWithFilesystemProbe(home, func(string) (bool, error) {
		return true, nil
	})
	if err != nil {
		t.Fatalf("sqliteConfigWithFilesystemProbe: %v", err)
	}
	parent, parentCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer parentCancel()
	ctx, cancel := config.AcquireContext(parent)
	defer cancel()
	remaining := time.Until(mustDeadline(t, ctx))
	if remaining > 5*time.Second || remaining <= 0 {
		t.Fatalf("caller deadline was replaced: %v", remaining)
	}
}

func TestHistoryAcquireContextBoundsRuntimeLikeRust(t *testing.T) {
	home := t.TempDir()
	config, err := sqliteConfigWithFilesystemProbe(home, func(string) (bool, error) {
		return true, nil
	})
	if err != nil {
		t.Fatalf("sqliteConfigWithFilesystemProbe: %v", err)
	}
	runtime := &StateRuntime{sqlite: config}
	ctx, cancel := runtime.historyAcquireContext(context.Background())
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("thread-history operations on a rollback-journal pool are not bounded")
	}

	local, err := sqliteConfigWithFilesystemProbe(t.TempDir(), func(string) (bool, error) {
		return false, nil
	})
	if err != nil {
		t.Fatalf("sqliteConfigWithFilesystemProbe: %v", err)
	}
	ctx, cancel = (&StateRuntime{sqlite: local}).historyAcquireContext(context.Background())
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("WAL thread-history operations gained an acquire deadline")
	}
}

func mustDeadline(t *testing.T, ctx context.Context) time.Time {
	t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("context has no deadline")
	}
	return deadline
}
