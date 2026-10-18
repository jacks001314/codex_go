//go:build !linux

package tui

import (
	"errors"
	"runtime"
)

// ReadPrimarySelection is unavailable outside Linux/X11/Wayland.
func ReadPrimarySelection() (string, error) {
	return "", errors.New("primary selection is unavailable on " + runtime.GOOS)
}

// WritePrimarySelection is unavailable outside Linux/X11/Wayland.
func WritePrimarySelection(text string) error {
	return errors.New("primary selection is unavailable on " + runtime.GOOS)
}
