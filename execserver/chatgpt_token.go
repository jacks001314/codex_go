package execserver

import (
	"bufio"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
)

const (
	// ChatGPTTokenMaxBytes mirrors Rust's 64 KiB bound on the token frame,
	// including its terminating newline.
	ChatGPTTokenMaxBytes = 64 * 1024
)

var (
	// ErrChatGPTTokenNotTerminated reports a token frame that did not end in a
	// newline within the size bound.
	ErrChatGPTTokenNotTerminated = errors.New("expected a bounded, newline-terminated ChatGPT access token")
	// ErrInvalidChatGPTToken reports a malformed or empty access token. The
	// rejected token is never included in the error.
	ErrInvalidChatGPTToken = errors.New("invalid ChatGPT access token")
)

// ReadChatGPTTokenFromReader reads one newline-terminated ChatGPT access token
// from a parent-owned pipe. It also returns the buffered remainder of the
// stream, which the caller may monitor for parent lifetime. The token is never
// written to this package or to auth.json.
func ReadChatGPTTokenFromReader(reader io.Reader) (string, io.Reader, error) {
	if reader == nil {
		return "", nil, ErrChatGPTTokenNotTerminated
	}
	if err := validateChatGPTTokenInput(reader); err != nil {
		return "", nil, err
	}
	if err := hardenRegistrationBearerRead(); err != nil {
		return "", nil, err
	}
	buffered := bufio.NewReader(io.LimitReader(reader, ChatGPTTokenMaxBytes))
	line, err := buffered.ReadString('\n')
	if err != nil && line == "" {
		return "", buffered, ErrChatGPTTokenNotTerminated
	}
	if !strings.HasSuffix(line, "\n") {
		return "", buffered, ErrChatGPTTokenNotTerminated
	}
	token := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if token == "" || !isASCIIGraphicToken(token) {
		return "", buffered, ErrInvalidChatGPTToken
	}
	return token, buffered, nil
}

func validateChatGPTTokenInput(reader io.Reader) error {
	file, ok := reader.(*os.File)
	if !ok {
		return nil
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return errors.New("ChatGPT token stdin must be a pipe or socket")
	}
	// Rust accepts any non-terminal stdin on non-Unix platforms; on Unix it
	// additionally requires a FIFO or socket so a regular file cannot supply a
	// long-lived credential.
	if runtime.GOOS != "windows" && info.Mode()&(os.ModeNamedPipe|os.ModeSocket) == 0 {
		return errors.New("ChatGPT token stdin must be a pipe or socket")
	}
	return nil
}
