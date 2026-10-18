//go:build linux

package appserverdaemon

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func readNativePIDProcessIdentity(pid uint32) (*ProcessIdentity, error) {
	state, ticks, err := readLinuxProcStat(pid)
	if err != nil {
		return nil, err
	}
	if state == "Z" {
		// A zombie is handled by the legacy path, which also reaps it.
		return &ProcessIdentity{BootID: readLinuxBootIDOrEmpty(), StartTicks: uint64Pointer(ticks)}, nil
	}
	bootID, err := readLinuxBootID()
	if err != nil {
		return nil, err
	}
	return &ProcessIdentity{BootID: bootID, StartTicks: uint64Pointer(ticks)}, nil
}

func readLinuxProcStat(pid uint32) (string, uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", 0, fmt.Errorf("failed to read process stat for pid-managed process %d: %w", pid, err)
	}
	return parseLinuxProcStat(data)
}

// parseLinuxProcStat parses state and starttime (field 22). The comm field can
// contain spaces and parentheses, so split after its final closing parenthesis.
func parseLinuxProcStat(data []byte) (string, uint64, error) {
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return "", 0, errors.New("process stat has no comm")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 {
		return "", 0, errors.New("process stat has no start time")
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("process stat start time is invalid: %w", err)
	}
	return fields[0], ticks, nil
}

func readLinuxBootID() (string, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", fmt.Errorf("failed to read Linux boot ID: %w", err)
	}
	bootID := strings.TrimSpace(string(data))
	if bootID == "" {
		return "", errors.New("Linux boot ID is empty")
	}
	return bootID, nil
}

func readLinuxBootIDOrEmpty() string {
	bootID, _ := readLinuxBootID()
	return bootID
}

// matchNativePIDProcessIdentity compares a stored native identity with the live
// process. The third result reports whether native matching is available.
func matchNativePIDProcessIdentity(identity *ProcessIdentity, pid uint32) (bool, bool, bool, error) {
	if identity == nil || identity.StartTicks == nil {
		return false, false, false, nil
	}
	bootID, err := readLinuxBootID()
	if err != nil {
		return false, false, true, err
	}
	if identity.BootID != "" && identity.BootID != bootID {
		return false, false, true, nil
	}
	state, ticks, err := readLinuxProcStat(pid)
	if err != nil {
		return false, false, true, err
	}
	return ticks == *identity.StartTicks, state == "Z", true, nil
}

// PromoteLegacyIdentity records a native identity on a verifiable legacy
// app-server record. The updater owns this promotion at startup (Rust #45779).
func (b *PIDBackend) PromoteLegacyIdentity() error {
	if b == nil {
		return nil
	}
	state, err := ReadPIDFileState(b.PIDFile)
	if err != nil || state.Kind != PIDFileRunning || state.Record == nil {
		return nil
	}
	if state.Record.ProcessIdentity != nil && state.Record.ProcessIdentity.StartTicks != nil {
		return nil
	}
	active, err := processMatchesPIDRecord(state.Record)
	if err != nil || !active {
		return nil
	}
	before, err := readNativePIDProcessIdentity(state.Record.PID)
	if err != nil {
		return nil
	}
	after, err := readNativePIDProcessIdentity(state.Record.PID)
	if err != nil || before == nil || after == nil ||
		before.BootID != after.BootID || before.StartTicks == nil || after.StartTicks == nil ||
		*before.StartTicks != *after.StartTicks {
		return nil
	}
	record := *state.Record
	record.ProcessIdentity = before
	return WritePIDRecord(b.PIDFile, &record)
}
