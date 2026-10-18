package tea

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	sysclipboard "github.com/atotto/clipboard"

	codextui "codex_go/tui"
	tuiapp "codex_go/tui/app"
)

const wslClipboardReaderScript = `
$ErrorActionPreference = 'Stop'
$out = [Console]::OpenStandardOutput()
$utf8 = [System.Text.UTF8Encoding]::new($false)
while ($null -ne [Console]::ReadLine()) {
    $bytes = $utf8.GetBytes([string](Get-Clipboard -Raw))
    $header = [BitConverter]::GetBytes([uint32]$bytes.Length)
    $out.Write($header, 0, $header.Length)
    $out.Write($bytes, 0, $bytes.Length)
    $out.Flush()
}
`

var errClipboardReaderBusy = errors.New("Windows clipboard reader is busy or unavailable")

type clipboardProcess struct {
	cmd    *exec.Cmd
	stdin  *os.File
	stdout *os.File
}

func (p *clipboardProcess) close() {
	if p == nil {
		return
	}
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	if p.stdout != nil {
		_ = p.stdout.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	}
}

type clipboardReadResult struct {
	text string
	err  error
}

type clipboardReadRequest struct {
	warm     bool
	deadline time.Time
	response chan<- clipboardReadResult
}

// clipboardCommandReader keeps one framed command process alive across reads.
// The command protocol is a little-endian uint32 byte length followed by UTF-8
// bytes, which preserves empty strings, whitespace, and separator runs.
type clipboardCommandReader struct {
	start    func() (*clipboardProcess, error)
	requests chan clipboardReadRequest
	closeCh  chan struct{}
	done     chan struct{}
	closeOne sync.Once
}

func newClipboardCommandReader(start func() (*clipboardProcess, error)) *clipboardCommandReader {
	reader := &clipboardCommandReader{
		start:    start,
		requests: make(chan clipboardReadRequest, 1),
		closeCh:  make(chan struct{}),
		done:     make(chan struct{}),
	}
	go reader.run()
	return reader
}

func (r *clipboardCommandReader) Read() (string, error) {
	return r.readUntil(time.Now().Add(tuiapp.RightClickPasteReadTimeout))
}

func (r *clipboardCommandReader) readUntil(deadline time.Time) (string, error) {
	if r == nil {
		return "", errClipboardReaderBusy
	}
	select {
	case <-r.done:
		return "", errClipboardReaderBusy
	default:
	}
	response := make(chan clipboardReadResult, 1)
	request := clipboardReadRequest{
		deadline: deadline,
		response: response,
	}
	select {
	case r.requests <- request:
	default:
		return "", errClipboardReaderBusy
	}
	return waitClipboardRead(request.deadline, response)
}

func (r *clipboardCommandReader) Warm() {
	if r == nil {
		return
	}
	select {
	case r.requests <- clipboardReadRequest{warm: true}:
	default:
	}
}

func (r *clipboardCommandReader) Close() {
	if r == nil {
		return
	}
	r.closeOne.Do(func() {
		close(r.closeCh)
	})
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
	}
}

func (r *clipboardCommandReader) run() {
	var process *clipboardProcess
	defer func() {
		if process != nil {
			process.close()
		}
		close(r.done)
	}()
	for {
		var request clipboardReadRequest
		var ok bool
		select {
		case <-r.closeCh:
			return
		case request, ok = <-r.requests:
			if !ok {
				return
			}
		}
		if request.warm {
			if process == nil {
				process, _ = r.ensureProcess()
			}
			continue
		}
		text, err := r.readWithProcess(&process, request.deadline)
		if err != nil && process != nil {
			process.close()
			process = nil
		}
		if request.response != nil {
			request.response <- clipboardReadResult{text: text, err: err}
		}
	}
}

func (r *clipboardCommandReader) ensureProcess() (*clipboardProcess, error) {
	if r == nil || r.start == nil {
		return nil, errClipboardReaderBusy
	}
	return r.start()
}

func (r *clipboardCommandReader) readWithProcess(process **clipboardProcess, deadline time.Time) (string, error) {
	if *process == nil {
		started, err := r.ensureProcess()
		if err != nil {
			return "", err
		}
		*process = started
	}
	if _, err := io.WriteString((*process).stdin, "\n"); err != nil {
		return "", err
	}
	_ = (*process).stdout.SetReadDeadline(deadline)
	var header [4]byte
	if _, err := io.ReadFull((*process).stdout, header[:]); err != nil {
		return "", err
	}
	size := binary.LittleEndian.Uint32(header[:])
	if size > tuiapp.RightClickPasteMaxTextBytes {
		return "", errors.New("clipboard text exceeds the message size limit")
	}
	payload := make([]byte, int(size))
	if _, err := io.ReadFull((*process).stdout, payload); err != nil {
		return "", err
	}
	if !utf8.Valid(payload) {
		return "", errors.New("clipboard text is not UTF-8")
	}
	return string(payload), nil
}

func waitClipboardRead(deadline time.Time, response <-chan clipboardReadResult) (string, error) {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return "", tuiapp.ErrRightClickPasteReadTimeout
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case result := <-response:
		return result.text, result.err
	case <-timer.C:
		return "", tuiapp.ErrRightClickPasteReadTimeout
	}
}

func startWSLClipboardProcess() (*clipboardProcess, error) {
	executable, err := exec.LookPath("powershell.exe")
	if err != nil {
		return nil, errors.New("Windows clipboard reader is unavailable")
	}
	command := exec.Command(executable, "-NoProfile", "-NonInteractive", "-Command", wslClipboardReaderScript)
	return startClipboardProcess(command)
}

func startClipboardProcess(command *exec.Cmd) (*clipboardProcess, error) {
	if command == nil {
		return nil, errClipboardReaderBusy
	}
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		_ = stdinRead.Close()
		_ = stdinWrite.Close()
		return nil, err
	}
	command.Stdin = stdinRead
	command.Stdout = stdoutWrite
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		_ = stdinRead.Close()
		_ = stdinWrite.Close()
		_ = stdoutRead.Close()
		_ = stdoutWrite.Close()
		return nil, errors.New("could not start Windows clipboard reader")
	}
	_ = stdinRead.Close()
	_ = stdoutWrite.Close()
	return &clipboardProcess{cmd: command, stdin: stdinWrite, stdout: stdoutRead}, nil
}

var (
	defaultClipboardReaderMu sync.Mutex
	defaultClipboardReader   *clipboardCommandReader
)

func defaultClipboardRead() func() (string, error) {
	if !codextui.IsProbablyWSL() ||
		strings.TrimSpace(os.Getenv("SSH_TTY")) != "" ||
		strings.TrimSpace(os.Getenv("SSH_CONNECTION")) != "" {
		return sysclipboard.ReadAll
	}
	defaultClipboardReaderMu.Lock()
	defer defaultClipboardReaderMu.Unlock()
	if defaultClipboardReader == nil {
		defaultClipboardReader = newClipboardCommandReader(startWSLClipboardProcess)
	}
	reader := defaultClipboardReader
	return reader.Read
}

func warmDefaultClipboardReader() {
	defaultClipboardReaderMu.Lock()
	defer defaultClipboardReaderMu.Unlock()
	if defaultClipboardReader != nil {
		defaultClipboardReader.Warm()
	}
}

func closeDefaultClipboardReader() {
	defaultClipboardReaderMu.Lock()
	reader := defaultClipboardReader
	defaultClipboardReader = nil
	defaultClipboardReaderMu.Unlock()
	if reader != nil {
		reader.Close()
	}
}

func shouldWarmClipboardReader(customReader bool, ownedScreen bool, environment tuiapp.PasteEnvironment, mode tuiapp.RightClickPasteMode) bool {
	return !customReader && ownedScreen && environment.WSL && environment.Allows(mode)
}
