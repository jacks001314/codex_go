package elevated

import (
	"os"
	"strings"

	"github.com/google/uuid"
)

const (
	PipeAccessInbound  uint32 = 0x00000001
	PipeAccessOutbound uint32 = 0x00000002
	PipeAccessDuplex   uint32 = 0x00000003
)

type RunnerPipe struct {
	Name   string
	Handle uintptr
}

func CreateRunnerPipe(name string) (*RunnerPipe, error) {
	username, err := CurrentUsername()
	if err != nil {
		return nil, err
	}
	return CreateNamedPipe(name, PipeAccessDuplex, username)
}

func PipePair() (string, string) {
	nonce := strings.ReplaceAll(uuid.NewString(), "-", "")
	base := `\\.\pipe\codex-runner-` + nonce
	return base + "-in", base + "-out"
}

func FindRunnerExe(codexHome string, currentExe string) string {
	if strings.TrimSpace(currentExe) == "" {
		if exe, err := os.Executable(); err == nil {
			currentExe = exe
		}
	}
	return resolveHelperForLaunch(codexHome, currentExe)
}
