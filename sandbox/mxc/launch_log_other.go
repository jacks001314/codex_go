//go:build !windows

package mxc

// logLaunch is a no-op where the MXC launcher cannot run.
func logLaunch(string) {}

// runtimeLogCodexHome has no launcher runtime log off Windows.
func runtimeLogCodexHome() string { return "" }
