//go:build darwin

package appserverdaemon

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	darwinProcInfoCallPIDInfo         = 2
	darwinProcPIDUniqueIdentifierInfo = 17
)

type darwinProcUniqueInfo struct {
	ExecutableUUID [16]byte
	UniqueID       uint64
	ParentUniqueID uint64
	Reserved       [3]uint64
}

func readNativePIDProcessIdentity(pid uint32) (*ProcessIdentity, error) {
	bootID, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return nil, fmt.Errorf("failed to read macOS boot ID: %w", err)
	}
	uniqueBefore, err := readDarwinUniqueID(pid)
	if err != nil {
		return nil, err
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", int(pid))
	if err != nil {
		return nil, fmt.Errorf("failed to read daemon process identity: %w", err)
	}
	if info == nil || info.Proc.P_pid != int32(pid) {
		return nil, fmt.Errorf("failed to read daemon process identity")
	}
	uniqueAfter, err := readDarwinUniqueID(pid)
	if err != nil {
		return nil, err
	}
	if uniqueBefore != uniqueAfter {
		return nil, fmt.Errorf("daemon process changed while reading its identity")
	}
	startSeconds := uint64(info.Proc.P_starttime.Sec)
	startMicroseconds := uint64(info.Proc.P_starttime.Usec)
	uniqueID := uint64(uniqueAfter)
	return &ProcessIdentity{
		BootID:            bootID,
		UniqueID:          &uniqueID,
		StartSeconds:      &startSeconds,
		StartMicroseconds: &startMicroseconds,
	}, nil
}

func matchNativePIDProcessIdentity(identity *ProcessIdentity, pid uint32) (bool, bool, bool, error) {
	if identity == nil {
		return false, false, false, nil
	}
	live, err := readNativePIDProcessIdentity(pid)
	if err != nil {
		return false, false, true, err
	}
	info, infoErr := unix.SysctlKinfoProc("kern.proc.pid", int(pid))
	zombie := infoErr == nil && info != nil && info.Proc.P_stat == 5
	matched := false
	if identity.UniqueID != nil && identity.BootID != "" && live.UniqueID != nil {
		matched = *identity.UniqueID == *live.UniqueID && identity.BootID == live.BootID
	} else if identity.StartSeconds != nil && identity.StartMicroseconds != nil &&
		live.StartSeconds != nil && live.StartMicroseconds != nil {
		matched = *identity.StartSeconds == *live.StartSeconds &&
			*identity.StartMicroseconds == *live.StartMicroseconds
	}
	return matched, zombie, true, nil
}

func readDarwinUniqueID(pid uint32) (uint64, error) {
	var info darwinProcUniqueInfo
	size := unsafe.Sizeof(info)
	result, _, errno := syscall.Syscall6(
		syscall.SYS_PROC_INFO,
		darwinProcInfoCallPIDInfo,
		uintptr(pid),
		darwinProcPIDUniqueIdentifierInfo,
		1,
		uintptr(unsafe.Pointer(&info)),
		size,
	)
	if errno != 0 {
		return 0, fmt.Errorf("failed to read daemon unique ID: %w", errno)
	}
	if result != size {
		return 0, fmt.Errorf("incomplete daemon unique ID")
	}
	return info.UniqueID, nil
}

func (b *PIDBackend) PromoteLegacyIdentity() error {
	if b == nil {
		return nil
	}
	state, err := ReadPIDFileState(b.PIDFile)
	if err != nil || state.Kind != PIDFileRunning || state.Record == nil {
		return nil
	}
	if state.Record.ProcessIdentity != nil &&
		state.Record.ProcessIdentity.UniqueID != nil &&
		state.Record.ProcessIdentity.BootID != "" {
		return nil
	}
	active, err := processMatchesPIDRecord(state.Record)
	if err != nil || !active {
		return nil
	}
	before, err := readNativePIDProcessIdentity(state.Record.PID)
	if err != nil || before == nil {
		return nil
	}
	after, err := readNativePIDProcessIdentity(state.Record.PID)
	if err != nil || after == nil ||
		before.UniqueID == nil || after.UniqueID == nil ||
		before.BootID == "" || before.BootID != after.BootID ||
		*before.UniqueID != *after.UniqueID {
		return nil
	}
	record := *state.Record
	record.ProcessIdentity = before
	return WritePIDRecord(b.PIDFile, &record)
}
