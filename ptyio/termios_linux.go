//go:build linux || solaris

package ptyio

import "golang.org/x/sys/unix"

// terminalTermios reads the terminal attributes with the Linux request code.
func terminalTermios(fd int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(fd, unix.TCGETS)
}
