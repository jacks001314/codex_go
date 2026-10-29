package processgroup

import (
	"errors"
	"syscall"
	"testing"
)

// TestSendRetriesDeniedGroupSignalAgainstMembersLikeRust mirrors Rust's
// signal_process_group_with_member_fallback (#46521): a denied group signal
// retries every member that still belongs to exactly that group, skipping
// members that moved elsewhere or exited, and signalling the leader last.
func TestSendRetriesDeniedGroupSignalAgainstMembersLikeRust(t *testing.T) {
	var signalled []int
	groups := map[int]int{10: 10, 11: 10, 12: 12, 13: 10}
	signals := fallbackSignals{
		group: func(int, syscall.Signal) (bool, error) {
			return false, syscall.EPERM
		},
		members: func(int) ([]int, error) {
			return []int{10, 11, 12, 13, 14, 0, -1}, nil
		},
		groupOf: func(pid int) (int, error) {
			if pid == 14 {
				return 0, syscall.ESRCH
			}
			return groups[pid], nil
		},
		process: func(pid int, sig syscall.Signal) (bool, error) {
			if sig != syscall.SIGTERM {
				t.Fatalf("member signal = %v, want SIGTERM", sig)
			}
			signalled = append(signalled, pid)
			return true, nil
		},
	}
	delivered, err := signals.send(10, syscall.SIGTERM)
	if err != nil || !delivered {
		t.Fatalf("send = (%t, %v), want (true, nil)", delivered, err)
	}
	want := []int{11, 13, 10}
	if len(signalled) != len(want) {
		t.Fatalf("signalled members = %#v, want %#v", signalled, want)
	}
	for index, pid := range want {
		if signalled[index] != pid {
			t.Fatalf("signalled members = %#v, want %#v", signalled, want)
		}
	}
}

// TestSendKeepsSuccessAndErrorPrecedenceLikeRust covers the remaining fallback
// outcomes: a delivered group signal never enumerates members, non-permission
// errors surface unchanged, invalid IDs are rejected, and a member's first
// error is reported only when no member accepted the signal.
func TestSendKeepsSuccessAndErrorPrecedenceLikeRust(t *testing.T) {
	enumerated := false
	delivered, err := fallbackSignals{
		group: func(int, syscall.Signal) (bool, error) { return false, nil },
		members: func(int) ([]int, error) {
			enumerated = true
			return nil, nil
		},
	}.send(10, syscall.SIGKILL)
	if err != nil || delivered || enumerated {
		t.Fatalf("gone group send = (%t, %v) enumerated=%t, want (false, nil) without enumeration", delivered, err, enumerated)
	}

	groupErr := errors.New("group signal failed")
	groupFailure := fallbackSignals{
		group: func(int, syscall.Signal) (bool, error) { return false, groupErr },
		members: func(int) ([]int, error) {
			t.Fatal("a non-permission group error must not enumerate members")
			return nil, nil
		},
	}
	if _, err := groupFailure.send(10, syscall.SIGTERM); !errors.Is(err, groupErr) {
		t.Fatalf("group error = %v, want %v", err, groupErr)
	}

	if _, err := (fallbackSignals{}).send(0, syscall.SIGTERM); !errors.Is(err, ErrInvalidProcessGroupID) {
		t.Fatalf("invalid group error = %v, want ErrInvalidProcessGroupID", err)
	}

	first := errors.New("first member error")
	second := errors.New("second member error")
	delivered, err = fallbackSignals{
		group:   func(int, syscall.Signal) (bool, error) { return false, syscall.EPERM },
		members: func(int) ([]int, error) { return []int{21, 22, 23}, nil },
		groupOf: func(int) (int, error) { return 10, nil },
		process: func(pid int, _ syscall.Signal) (bool, error) {
			switch pid {
			case 21:
				return false, first
			case 22:
				return false, second
			}
			return false, nil
		},
	}.send(10, syscall.SIGTERM)
	if delivered || !errors.Is(err, first) {
		t.Fatalf("failed member send = (%t, %v), want (false, first error)", delivered, err)
	}

	delivered, err = fallbackSignals{
		group:   func(int, syscall.Signal) (bool, error) { return false, syscall.EPERM },
		members: func(int) ([]int, error) { return []int{31, 32}, nil },
		groupOf: func(pid int) (int, error) {
			if pid == 31 {
				return 0, syscall.ESRCH
			}
			return 10, nil
		},
		process: func(int, syscall.Signal) (bool, error) { return true, nil },
	}.send(10, syscall.SIGTERM)
	if err != nil || !delivered {
		t.Fatalf("partial member send = (%t, %v), want (true, nil)", delivered, err)
	}
}
