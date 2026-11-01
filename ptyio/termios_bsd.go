//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package ptyio

import "golang.org/x/sys/unix"

// terminalTermios reads the terminal attributes with the BSD request code;
// TCGETS does not exist on these platforms.
func terminalTermios(fd int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(fd, unix.TIOCGETA)
}
