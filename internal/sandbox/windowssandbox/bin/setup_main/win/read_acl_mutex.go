package win

import "codex_go/internal/sandbox/windowssandbox"

const ReadACLMutexName = `Local\CodexSandboxReadAcl`

type ReadACLMutexGuard struct {
	handle uintptr
}

func WithReadACLMutex(fn func() error) error {
	if fn == nil {
		return windowssandbox.ErrInvalidRequest
	}
	guard, acquired, err := AcquireReadACLMutex()
	if err != nil {
		return err
	}
	if !acquired {
		return nil
	}
	defer guard.Close()
	return fn()
}
