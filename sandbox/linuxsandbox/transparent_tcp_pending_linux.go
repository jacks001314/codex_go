//go:build linux

package linuxsandbox

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	transparentTCPPendingInterval = 500 * time.Millisecond
	transparentTCPPendingTables   = 2
	transparentTCPPendingLimit    = 16384
)

var errTransparentTCPClientGone = fmt.Errorf("transparent TCP client socket is gone")

var transparentTCPPendingTableNames = [transparentTCPPendingTables]string{
	"/proc/self/net/tcp",
	"/proc/self/net/tcp6",
}

type transparentTCPPendingOwner struct {
	inode    uint64
	table    string
	captured time.Time
}

type transparentTCPPendingSnapshot struct {
	mu      sync.Mutex
	sampled time.Time
	inodes  map[uint64]struct{}
}

var transparentTCPPendingSnapshots [transparentTCPPendingTables]transparentTCPPendingSnapshot

// captureTransparentTCPPendingOwner pins the native socket inode before a
// destination is handed to the managed proxy. Linux keeps the inode while the
// client socket lives, and clears it on the last close.
func captureTransparentTCPPendingOwner(source, target netip.AddrPort) (*transparentTCPPendingOwner, error) {
	sourceKey, sourceV4 := transparentTCPProcKey(source)
	targetKey, targetV4 := transparentTCPProcKey(target)
	if sourceV4 && targetV4 {
		owner, err := captureTransparentTCPPendingFromTable(0, sourceKey, targetKey)
		if err != nil {
			return nil, err
		}
		if owner != nil {
			return owner, nil
		}
	}
	owner, err := captureTransparentTCPPendingFromTable(1, transparentTCPProcKeyV6(source), transparentTCPProcKeyV6(target))
	if err != nil {
		return nil, err
	}
	if owner == nil {
		return nil, errTransparentTCPClientGone
	}
	return owner, nil
}

func captureTransparentTCPPendingFromTable(index int, local, remote string) (*transparentTCPPendingOwner, error) {
	table := transparentTCPPendingTableNames[index]
	inode, found, err := scanTransparentTCPInodes(table, func(from, to string, inode uint64) bool {
		return inode != 0 && from == local && to == remote
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return &transparentTCPPendingOwner{inode: inode, table: table, captured: time.Now()}, nil
}

func transparentTCPProcKey(address netip.AddrPort) (string, bool) {
	addr := address.Addr().Unmap()
	if !addr.Is4() {
		return "", false
	}
	raw := addr.As4()
	return fmt.Sprintf("%08X:%04X", binary.NativeEndian.Uint32(raw[:]), address.Port()), true
}

func transparentTCPProcKeyV6(address netip.AddrPort) string {
	addr := address.Addr()
	if addr.Is4() {
		addr = netip.AddrFrom16(addr.As16())
	}
	raw := addr.As16()
	var builder strings.Builder
	for offset := 0; offset < len(raw); offset += 4 {
		fmt.Fprintf(&builder, "%08X", binary.NativeEndian.Uint32(raw[offset:offset+4]))
	}
	fmt.Fprintf(&builder, ":%04X", address.Port())
	return builder.String()
}

// scanTransparentTCPInodes walks one procfs table. It stops as soon as match
// accepts a record, or after it has collected limit distinct inodes.
func scanTransparentTCPInodes(table string, match func(local, remote string, inode uint64) bool) (uint64, bool, error) {
	file, err := os.Open(table)
	if err != nil {
		return 0, false, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() || !strings.Contains(scanner.Text(), "local_address") || !strings.Contains(scanner.Text(), "inode") {
		return 0, false, fmt.Errorf("missing native TCP socket table in %s", table)
	}
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		local, remote, inodeField := fields[1], fields[2], fields[9]
		inode, err := strconv.ParseUint(inodeField, 10, 64)
		if err != nil {
			return 0, false, fmt.Errorf("parse native TCP socket inode: %w", err)
		}
		if match(local, remote, inode) {
			return inode, true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

// stillOwned reports whether the captured socket inode is still present. Slow
// approvals share one bounded snapshot per address family.
func (o *transparentTCPPendingOwner) stillOwned() (bool, error) {
	index := 0
	if o.table == transparentTCPPendingTableNames[1] {
		index = 1
	}
	snapshot := &transparentTCPPendingSnapshots[index]
	snapshot.mu.Lock()
	if snapshot.inodes == nil || snapshot.sampled.Before(o.captured) || time.Since(snapshot.sampled) >= transparentTCPPendingInterval {
		sampled := time.Now()
		inodes, complete, err := transparentTCPInodeSnapshot(o.table)
		if err != nil {
			snapshot.mu.Unlock()
			return false, err
		}
		if complete {
			snapshot.inodes = inodes
		} else {
			snapshot.inodes = nil
		}
		snapshot.sampled = sampled
	}
	if snapshot.inodes != nil {
		_, ok := snapshot.inodes[o.inode]
		snapshot.mu.Unlock()
		return ok, nil
	}
	snapshot.mu.Unlock()
	_, found, err := scanTransparentTCPInodes(o.table, func(_, _ string, inode uint64) bool {
		return inode == o.inode
	})
	if err != nil {
		return false, err
	}
	return found, nil
}

func transparentTCPInodeSnapshot(table string) (map[uint64]struct{}, bool, error) {
	file, err := os.Open(table)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return nil, false, fmt.Errorf("missing native TCP socket table in %s", table)
	}
	inodes := make(map[uint64]struct{}, 128)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}
		inode, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			return nil, false, fmt.Errorf("parse native TCP socket inode: %w", err)
		}
		if inode != 0 {
			inodes[inode] = struct{}{}
		}
		if len(inodes) > transparentTCPPendingLimit {
			return nil, false, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, false, err
	}
	return inodes, true, nil
}
