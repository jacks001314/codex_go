//go:build !windows

package mcp

// reportMCPHelperConsoleState is Windows-only: the console-window suppression it
// observes has no counterpart elsewhere.
func reportMCPHelperConsoleState() {}
