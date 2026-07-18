package appserver

import (
	"bytes"
	"os/exec"
	"sync"
)

type lockedOutputBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *lockedOutputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}

func (b *lockedOutputBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}

func (b *lockedOutputBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data.Bytes()...)
}

func runCommandCaptured(cmd *exec.Cmd) (stdout string, stderr string, err error) {
	var stdoutBuffer lockedOutputBuffer
	var stderrBuffer lockedOutputBuffer
	cmd.Stdout = &stdoutBuffer
	cmd.Stderr = &stderrBuffer
	err = cmd.Run()
	return stdoutBuffer.String(), stderrBuffer.String(), err
}

func runCommandOutput(cmd *exec.Cmd) ([]byte, error) {
	var stdout lockedOutputBuffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	return stdout.Bytes(), err
}

func runCommandCombinedOutput(cmd *exec.Cmd) ([]byte, error) {
	var stdout lockedOutputBuffer
	var stderr lockedOutputBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := stdout.Bytes()
	out = append(out, stderr.Bytes()...)
	return out, err
}
