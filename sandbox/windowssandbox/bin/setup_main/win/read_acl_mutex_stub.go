//go:build !windows

package win

import "codex_go/sandbox/windowssandbox"

func ReadACLMutexExists() (bool, error) {
	return false, windowssandbox.Unsupported("bin.setup_main.win.read_acl_mutex.exists")
}

func AcquireReadACLMutex() (*ReadACLMutexGuard, bool, error) {
	return nil, false, windowssandbox.Unsupported("bin.setup_main.win.read_acl_mutex.acquire")
}

func (g *ReadACLMutexGuard) Close() error {
	return nil
}
