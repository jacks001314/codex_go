//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package tea

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type unixJobControlSession struct {
	tty  *os.File
	pgid int
}

func openJobControlSession() (jobControlSession, error) {
	// /dev/tty requires a controlling terminal, unlike an arbitrary terminal
	// descriptor on stdin. Keep it open through suspend/resume.
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("no controlling terminal: %w", err)
	}
	pgid := unix.Getpgrp()
	if pgid <= 0 {
		tty.Close()
		return nil, fmt.Errorf("invalid process group %d", pgid)
	}
	return &unixJobControlSession{tty: tty, pgid: pgid}, nil
}

func (s *unixJobControlSession) foreground() (bool, error) {
	pgid, err := unix.IoctlGetInt(int(s.tty.Fd()), unix.TIOCGPGRP)
	if err != nil {
		return false, fmt.Errorf("query foreground terminal owner: %w", err)
	}
	if pgid <= 0 {
		return false, fmt.Errorf("terminal has no foreground process group")
	}
	return pgid == s.pgid, nil
}

func (s *unixJobControlSession) suspend(ctx context.Context) error {
	continued := make(chan os.Signal, 1)
	signal.Notify(continued, syscall.SIGCONT)
	defer signal.Stop(continued)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := unix.Kill(-s.pgid, unix.SIGTSTP); err != nil {
		return fmt.Errorf("send suspend signal: %w", err)
	}
	// Count scheduled ticks instead of using a wall deadline: a Go timer can
	// expire while the entire process group is stopped. A ticker coalesces
	// those missed ticks, leaving time for SIGCONT delivery after a long stop.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for ticks := 0; ticks < 100; {
		select {
		case <-continued:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			ticks++
		}
	}
	return fmt.Errorf("timed out waiting for SIGCONT")
}

func (s *unixJobControlSession) close() { s.tty.Close() }
