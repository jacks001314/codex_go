//go:build linux

package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ReadPrimarySelection reads X11 PRIMARY/Wayland primary selection using the
// first available clipboard helper (Rust #49112/#53753).
func ReadPrimarySelection() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errs := []string{}
	for _, candidate := range []struct {
		name string
		args []string
	}{
		{name: "wl-paste", args: []string{"--primary", "--no-newline"}},
		{name: "xclip", args: []string{"-selection", "primary", "-o"}},
		{name: "xsel", args: []string{"--primary", "--output"}},
	} {
		output, err := exec.CommandContext(ctx, candidate.name, candidate.args...).Output()
		if err == nil {
			return string(output), nil
		}
		errs = append(errs, candidate.name+": "+err.Error())
	}
	return "", fmt.Errorf("primary selection unavailable: %s", strings.Join(errs, "; "))
}

// WritePrimarySelection publishes text to X11 PRIMARY/Wayland primary selection.
func WritePrimarySelection(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errs := []string{}
	for _, candidate := range []struct {
		name string
		args []string
	}{
		{name: "wl-copy", args: []string{"--primary"}},
		{name: "xclip", args: []string{"-selection", "primary", "-in"}},
		{name: "xsel", args: []string{"--primary", "--input"}},
	} {
		command := exec.CommandContext(ctx, candidate.name, candidate.args...)
		command.Stdin = strings.NewReader(text)
		if err := command.Run(); err == nil {
			return nil
		} else {
			errs = append(errs, candidate.name+": "+err.Error())
		}
	}
	return fmt.Errorf("primary selection unavailable: %s", strings.Join(errs, "; "))
}

// ReleasePrimarySelection clears the primary selection on focus loss/resume.
func ReleasePrimarySelection() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errs := []string{}
	for _, candidate := range []struct {
		name string
		args []string
	}{
		{name: "wl-copy", args: []string{"--clear", "--primary"}},
		{name: "xsel", args: []string{"--primary", "--clear"}},
	} {
		if err := exec.CommandContext(ctx, candidate.name, candidate.args...).Run(); err == nil {
			return nil
		} else {
			errs = append(errs, candidate.name+": "+err.Error())
		}
	}
	return fmt.Errorf("primary selection release unavailable: %s", strings.Join(errs, "; "))
}
