//go:build windows

package mxc

import (
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// helperSupported reports whether this host can execute the native MXC
// sandbox at all (Rust `mxc_sandbox::is_available` on non-Windows hosts).
const helperSupported = true

// NativeUnavailableMessage mirrors the executor's fail-closed diagnostic. Go
// has no binding to MXC's native launcher, so the helper validates the whole
// request and then refuses to launch instead of falling back to a weaker
// sandbox backend.
const NativeUnavailableMessage = "native MXC is unavailable on this executor"

// runHelper resolves executor-local state and runs the decoded launcher
// request (Rust `mxc_sandbox::windows::run`).
func runHelper(stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	env, err := currentEnvironment()
	if err != nil {
		return 0, err
	}
	request, err := DecodeCommandArgs(env)
	if err != nil {
		return 0, err
	}
	commandCWD, err := os.Getwd()
	if err != nil {
		return 0, err
	}
	native, err := BuildRequest(request, commandCWD, env)
	if err != nil {
		return 0, err
	}
	return launch(native, stdin, stdout, stderr)
}

// launch hands a prepared request to the native launcher and propagates the
// child exit code. Rust gates this on `is_available()`; Go encodes that gate as
// the durable architectural gap rather than silently degrading the sandbox.
func launch(request *NativeRequest, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	if request == nil {
		return 0, errors.New("invalid MXC launcher request")
	}
	return 0, errors.New(NativeUnavailableMessage)
}

// currentEnvironment reads the already-filtered wrapper environment. Rust
// rejects lossy values instead of inheriting a different environment inside the
// sandbox, so names and values are decoded strictly from the UTF-16 block.
func currentEnvironment() (map[string]string, error) {
	block, err := windows.GetEnvironmentStrings()
	if err != nil {
		return nil, fmt.Errorf("read environment: %w", err)
	}
	defer func() { _ = windows.FreeEnvironmentStrings(block) }()
	return decodeEnvironmentBlock(block)
}

// decodeEnvironmentBlock parses a double-NUL-terminated UTF-16 environment
// block. Entries starting with '=' are the per-drive current-directory values
// Windows keeps in the block; they are not variables.
func decodeEnvironmentBlock(block *uint16) (map[string]string, error) {
	env := make(map[string]string)
	cursor := block
	for *cursor != 0 {
		var units []uint16
		for *cursor != 0 {
			units = append(units, *cursor)
			cursor = (*uint16)(unsafe.Add(unsafe.Pointer(cursor), unsafe.Sizeof(*cursor)))
		}
		cursor = (*uint16)(unsafe.Add(unsafe.Pointer(cursor), unsafe.Sizeof(*cursor)))
		separator := -1
		for index, unit := range units {
			if unit == '=' {
				separator = index
				break
			}
		}
		if separator == 0 {
			continue
		}
		if separator < 0 {
			return nil, errors.New("MXC requires Unicode environment variable names")
		}
		name, ok := decodeUTF16(units[:separator])
		if !ok {
			return nil, errors.New("MXC requires Unicode environment variable names")
		}
		value, ok := decodeUTF16(units[separator+1:])
		if !ok {
			return nil, errors.New("MXC requires Unicode environment variable values")
		}
		env[name] = value
	}
	return env, nil
}

// decodeUTF16 reports whether the units form well-formed UTF-16. Go's
// syscall.UTF16ToString silently replaces unpaired surrogates, which would
// change the child environment.
func decodeUTF16(units []uint16) (string, bool) {
	runes := make([]rune, 0, len(units))
	for index := 0; index < len(units); index++ {
		unit := units[index]
		switch {
		case unit >= 0xD800 && unit <= 0xDBFF:
			if index+1 >= len(units) {
				return "", false
			}
			next := units[index+1]
			if next < 0xDC00 || next > 0xDFFF {
				return "", false
			}
			runes = append(runes, utf16.DecodeRune(rune(unit), rune(next)))
			index++
		case unit >= 0xDC00 && unit <= 0xDFFF:
			return "", false
		default:
			runes = append(runes, rune(unit))
		}
	}
	return string(runes), true
}
