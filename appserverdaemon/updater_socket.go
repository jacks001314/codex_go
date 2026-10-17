package appserverdaemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"codex_go/codexuds"
)

var (
	managedUpdaterReadyTimeout      = 30 * time.Second
	managedUpdaterReadyPollInterval = PIDStartPollInterval
)

// bindManualUpdaterSocket uses an advertised rendezvous symlink on Unix so a
// deep CODEX_HOME cannot exceed the kernel's sun_path limit. Windows keeps its
// existing private-directory binding.
func bindManualUpdaterSocket(socketPath string) (*manualUpdateSocket, error) {
	if runtime.GOOS == "windows" {
		return bindDirectManualUpdaterSocket(socketPath)
	}
	return bindUnixManualUpdaterSocket(socketPath)
}

func bindUnixManualUpdaterSocket(socketPath string) (*manualUpdateSocket, error) {
	if err := codexuds.PreparePrivateSocketDirectory(filepath.Dir(socketPath)); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(socketPath); err == nil {
		if info.Mode()&(os.ModeSocket|os.ModeSymlink) == 0 {
			return nil, fmt.Errorf("updater socket path %s exists and is not a socket or symlink", socketPath)
		}
		if err := os.Remove(socketPath); err != nil {
			return nil, fmt.Errorf("failed to clear the stale updater socket %s: %w", socketPath, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	socketDir, err := os.MkdirTemp("", "codex-updater-")
	if err != nil {
		return nil, fmt.Errorf("failed to create the updater socket directory: %w", err)
	}
	physicalPath := filepath.Join(socketDir, "app-server-updater.sock")
	listener, err := net.Listen("unix", physicalPath)
	if err != nil {
		_ = os.Remove(socketDir)
		return nil, fmt.Errorf("failed to bind the updater request socket %s: %w", socketPath, err)
	}
	if err := os.Symlink(physicalPath, socketPath); err != nil {
		_ = listener.Close()
		_ = os.Remove(physicalPath)
		_ = os.Remove(socketDir)
		return nil, fmt.Errorf("failed to publish the updater socket %s: %w", socketPath, err)
	}
	return &manualUpdateSocket{
		Listener: listener,
		Path:     socketPath,
		cleanup: func() error {
			var errs []error
			if target, err := os.Readlink(socketPath); err == nil && target == physicalPath {
				if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
					errs = append(errs, err)
				}
			}
			if err := os.Remove(physicalPath); err != nil && !os.IsNotExist(err) {
				errs = append(errs, err)
			}
			if err := os.Remove(socketDir); err != nil && !os.IsNotExist(err) {
				errs = append(errs, err)
			}
			return errors.Join(errs...)
		},
	}, nil
}

func bindDirectManualUpdaterSocket(socketPath string) (*manualUpdateSocket, error) {
	if err := codexuds.PreparePrivateSocketDirectory(filepath.Dir(socketPath)); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(socketPath); err == nil {
		if err := os.Remove(socketPath); err != nil {
			return nil, fmt.Errorf("failed to clear the stale updater socket %s: %w", socketPath, err)
		}
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to bind the updater request socket %s: %w", socketPath, err)
	}
	return &manualUpdateSocket{
		Listener: listener,
		Path:     socketPath,
		cleanup: func() error {
			if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
				return err
			}
			return nil
		},
	}, nil
}

// waitForManagedUpdaterSocketReady waits for the updater process to publish its
// rendezvous socket. It never connects, because an empty probe can consume a
// one-shot updater's restore authority (Rust #53845).
func waitForManagedUpdaterSocketReady(backend *PIDBackend, socketPath string, running func(*PIDBackend) (bool, error)) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	return waitForManagedUpdaterSocketReadyUnix(backend, socketPath, running)
}

func waitForManagedUpdaterSocketReadyUnix(backend *PIDBackend, socketPath string, running func(*PIDBackend) (bool, error)) error {
	deadline := time.Now().Add(managedUpdaterReadyTimeout)
	for {
		active, err := running(backend)
		if err != nil {
			return err
		}
		info, statErr := os.Stat(socketPath)
		if active && statErr == nil && info.Mode()&os.ModeSocket != 0 {
			return nil
		}
		if !active {
			return fmt.Errorf("daemon updater exited before becoming ready on %s", socketPath)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("daemon updater timed out before becoming ready on %s", socketPath)
		}
		time.Sleep(managedUpdaterReadyPollInterval)
	}
}
