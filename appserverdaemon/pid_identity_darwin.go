//go:build darwin

package appserverdaemon

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func readNativePIDProcessIdentity(pid uint32) (*ProcessIdentity, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", int(pid))
	if err != nil {
		return nil, fmt.Errorf("failed to read daemon process identity: %w", err)
	}
	if info == nil || info.Proc.P_pid != int32(pid) {
		return nil, fmt.Errorf("failed to read daemon process identity")
	}
	startSeconds := uint64(info.Proc.P_starttime.Sec)
	startMicroseconds := uint64(info.Proc.P_starttime.Usec)
	return &ProcessIdentity{StartSeconds: &startSeconds, StartMicroseconds: &startMicroseconds}, nil
}

func matchNativePIDProcessIdentity(identity *ProcessIdentity, pid uint32) (bool, bool, bool, error) {
	if identity == nil || identity.StartSeconds == nil || identity.StartMicroseconds == nil {
		return false, false, false, nil
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", int(pid))
	if err != nil {
		return false, false, true, err
	}
	if info == nil || info.Proc.P_pid != int32(pid) {
		return false, false, true, nil
	}
	matched := uint64(info.Proc.P_starttime.Sec) == *identity.StartSeconds &&
		uint64(info.Proc.P_starttime.Usec) == *identity.StartMicroseconds
	// SZOMB is 5 in XNU's process state enum.
	return matched, info.Proc.P_stat == 5, true, nil
}

func (b *PIDBackend) PromoteLegacyIdentity() error {
	if b == nil {
		return nil
	}
	state, err := ReadPIDFileState(b.PIDFile)
	if err != nil || state.Kind != PIDFileRunning || state.Record == nil {
		return nil
	}
	if state.Record.ProcessIdentity != nil && state.Record.ProcessIdentity.StartSeconds != nil {
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
		before.StartSeconds == nil || after.StartSeconds == nil ||
		before.StartMicroseconds == nil || after.StartMicroseconds == nil ||
		*before.StartSeconds != *after.StartSeconds ||
		*before.StartMicroseconds != *after.StartMicroseconds {
		return nil
	}
	record := *state.Record
	record.ProcessIdentity = before
	return WritePIDRecord(b.PIDFile, &record)
}
