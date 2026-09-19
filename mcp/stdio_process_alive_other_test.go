//go:build !unix && !windows

package mcp

// mcpTestProcessAlive cannot observe processes on this platform; containment is
// also unavailable, so the tree test skips.
func mcpTestProcessAlive(int) bool { return false }
