// Package processgroup mirrors Rust `codex_utils_pty::process_group`: shared
// signal helpers for processes that lead their own process group. On macOS a
// denied group signal is retried against the group's individual members, so
// cleanup still reaches every member of the tree (#46521).
package processgroup

import (
	"errors"
	"sort"
	"syscall"
)

// ErrInvalidProcessGroupID mirrors Rust's InvalidInput reason for a
// non-positive process group ID.
var ErrInvalidProcessGroupID = errors.New("invalid process group ID")

// signaler delivers one signal and reports whether an existing target accepted
// it. A missing target is reported as (false, nil).
type signaler func(target int, sig syscall.Signal) (bool, error)

// fallbackSignals is the injection point that keeps the macOS member fallback
// testable on every platform.
type fallbackSignals struct {
	group   signaler
	members func(pgid int) ([]int, error)
	groupOf func(pid int) (int, error)
	process signaler
}

// send mirrors Rust `signal_process_group_with_member_fallback`: signal the
// whole group and, when the kernel denies it, retry each member that still
// belongs to exactly that group. The group leader is signalled last so the
// group survives member signalling.
func (s fallbackSignals) send(pgid int, sig syscall.Signal) (bool, error) {
	if pgid <= 0 {
		return false, ErrInvalidProcessGroupID
	}
	delivered, err := s.group(pgid, sig)
	if err == nil {
		return delivered, nil
	}
	if !errors.Is(err, syscall.EPERM) {
		return false, err
	}
	members, err := s.members(pgid)
	if err != nil {
		return false, err
	}
	ordered := append([]int(nil), members...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return memberRank(ordered[i], pgid) < memberRank(ordered[j], pgid)
	})
	signalled := false
	var firstErr error
	for _, pid := range ordered {
		if pid <= 0 {
			continue
		}
		current, err := s.groupOf(pid)
		if err != nil {
			if !errors.Is(err, syscall.ESRCH) && firstErr == nil {
				firstErr = err
			}
			continue
		}
		if current != pgid {
			continue
		}
		delivered, err := s.process(pid, sig)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		signalled = signalled || delivered
	}
	if signalled {
		return true, nil
	}
	if firstErr != nil {
		return false, firstErr
	}
	return false, nil
}

// memberRank orders members so the process group leader is signalled last.
func memberRank(pid int, pgid int) int {
	if pid == pgid {
		return 1
	}
	return 0
}
