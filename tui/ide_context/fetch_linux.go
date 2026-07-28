//go:build linux

package idecontext

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func connectIDEContext(_ string, codexHome string, deadline time.Time) (io.ReadWriteCloser, error) {
	paths := make([]string, 0, 3)
	if codexHome != "" {
		paths = append(paths, filepath.Join(codexHome, "ipc", "ipc.sock"))
	}
	ipcDir := filepath.Join(os.TempDir(), "codex-ipc")
	if os.Getuid() == 0 {
		paths = append(paths, filepath.Join(ipcDir, "ipc.sock"), filepath.Join(ipcDir, "ipc-0.sock"))
	} else {
		paths = append(paths, filepath.Join(ipcDir, "ipc-"+unixUserID()+".sock"))
	}

	var lastErr error = errors.New("no IDE IPC socket paths were available")
	for _, path := range paths {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, ErrIDEContextTimedOut
		}
		connection, err := net.DialTimeout("unix", path, remaining)
		if err != nil {
			lastErr = err
			continue
		}
		if err := connection.SetDeadline(deadline); err != nil {
			connection.Close()
			return nil, err
		}
		if err := validateUnixPeerOwner(connection); err != nil {
			connection.Close()
			return nil, err
		}
		return connection, nil
	}
	return nil, lastErr
}

func validateUnixPeerOwner(connection net.Conn) error {
	syscallConn, ok := connection.(syscall.Conn)
	if !ok {
		return errors.New("IDE context socket credentials are unavailable")
	}
	raw, err := syscallConn.SyscallConn()
	if err != nil {
		return err
	}
	var credential *unix.Ucred
	var controlErr error
	if err := raw.Control(func(fd uintptr) {
		credential, controlErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if controlErr != nil {
		return controlErr
	}
	if credential == nil || credential.Uid != uint32(os.Getuid()) {
		return errors.New("IDE context provider is not owned by the current user")
	}
	return nil
}

func unixUserID() string {
	return fmtUint(uint64(os.Getuid()))
}

func fmtUint(value uint64) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	buffer := [20]byte{}
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = digits[value%10]
		value /= 10
	}
	return string(buffer[index:])
}
