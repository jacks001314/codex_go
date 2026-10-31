package prompt

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// StdinPromptBehavior mirrors codex-rs/exec/src/lib.rs::StdinPromptBehavior.
type StdinPromptBehavior int

const (
	// RequiredIfPiped reads stdin only when no positional prompt was given,
	// which is the legacy `codex exec` behavior for piped input.
	RequiredIfPiped StdinPromptBehavior = iota
	// Forced always treats stdin as the prompt (`codex exec -`).
	Forced
	// OptionalAppend treats piped stdin as extra context for a positional
	// prompt instead of replacing it.
	OptionalAppend
)

// DirectExitError is an error Rust prints verbatim with eprintln! before
// exiting, so it must not pick up the "Error: " prefix anyhow errors get.
type DirectExitError struct {
	Text string
}

func (e *DirectExitError) Error() string { return e.Text }

// DirectExitMessage reports the verbatim text for app.DirectExitMessenger.
func (e *DirectExitError) DirectExitMessage() string { return e.Text }

// ReadStdinPrompt mirrors exec::read_prompt_from_stdin: it prints Rust's
// stderr notices, reports whether stdin contributed text, and returns the same
// errors. `ok` is false only for OptionalAppend with nothing to append.
func ReadStdinPrompt(stdin io.Reader, stderr io.Writer, behavior StdinPromptBehavior) (string, bool, error) {
	stdinIsTerminal := IsTerminalReader(stdin)
	switch behavior {
	case RequiredIfPiped:
		if stdinIsTerminal {
			return "", false, &DirectExitError{Text: "No prompt provided. Either specify one as an argument or pipe the prompt into stdin."}
		}
		fmt.Fprintln(stderr, "Reading prompt from stdin...")
	case OptionalAppend:
		if stdinIsTerminal {
			return "", false, nil
		}
		fmt.Fprintln(stderr, "Reading additional input from stdin...")
	}

	text, err := readAllString(stdin)
	if err != nil {
		return "", false, fmt.Errorf("Failed to read prompt from stdin: %v", err)
	}
	if strings.TrimSpace(text) == "" {
		if behavior == OptionalAppend {
			return "", false, nil
		}
		return "", false, &DirectExitError{Text: "No prompt provided via stdin."}
	}
	return text, true, nil
}

// ResolveExecPrompt mirrors exec::resolve_prompt: a positional prompt is used
// verbatim (stdin is not read), `-` forces stdin, and a missing prompt reads
// piped stdin or fails when stdin is a terminal.
func ResolveExecPrompt(promptArg string, stdin io.Reader, stderr io.Writer) (string, error) {
	if promptArg != "" && promptArg != "-" {
		return promptArg, nil
	}
	behavior := RequiredIfPiped
	if promptArg == "-" {
		behavior = Forced
	}
	text, _, err := ReadStdinPrompt(stdin, stderr, behavior)
	return text, err
}

// ResolveRootPrompt mirrors exec::resolve_root_prompt: a positional prompt
// keeps piped stdin as a `<stdin>` context block, everything else falls through
// to ResolveExecPrompt.
func ResolveRootPrompt(promptArg string, stdin io.Reader, stderr io.Writer) (string, error) {
	if promptArg != "" && promptArg != "-" {
		text, ok, err := ReadStdinPrompt(stdin, stderr, OptionalAppend)
		if err != nil {
			return "", err
		}
		if !ok {
			return promptArg, nil
		}
		return WithStdinContext(promptArg, text), nil
	}
	return ResolveExecPrompt(promptArg, stdin, stderr)
}

// IsTerminalReader reports whether reading from reader would wait on a user.
func IsTerminalReader(reader io.Reader) bool {
	if terminal, ok := reader.(interface{ IsTerminal() bool }); ok {
		return terminal.IsTerminal()
	}
	file, ok := reader.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func Resolve(promptArg string, stdin io.Reader) (string, error) {
	stdinText, err := readAllString(stdin)
	if err != nil {
		return "", err
	}
	switch {
	case promptArg == "-":
		if strings.TrimSpace(stdinText) == "" {
			return "", errors.New("No prompt provided via stdin.")
		}
		return stdinText, nil
	case promptArg != "":
		if strings.TrimSpace(stdinText) == "" {
			return promptArg, nil
		}
		return WithStdinContext(promptArg, stdinText), nil
	default:
		if strings.TrimSpace(stdinText) == "" {
			return "", errors.New("No prompt provided. Either specify one as an argument or pipe the prompt into stdin.")
		}
		return stdinText, nil
	}
}

func WithStdinContext(prompt, stdinText string) string {
	var builder strings.Builder
	builder.WriteString(prompt)
	builder.WriteString("\n\n<stdin>\n")
	builder.WriteString(stdinText)
	if !strings.HasSuffix(stdinText, "\n") {
		builder.WriteByte('\n')
	}
	builder.WriteString("</stdin>")
	return builder.String()
}

func readAllString(reader io.Reader) (string, error) {
	if reader == nil {
		return "", nil
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return decodePromptBytes(data)
}

type promptDecodeError struct {
	kind      string
	validUpTo int
	encoding  string
}

func (e *promptDecodeError) Error() string {
	switch e.kind {
	case "invalid-utf8":
		return "input is not valid UTF-8 (invalid byte at offset " + strconv.Itoa(e.validUpTo) + "). Convert it to UTF-8 and retry (e.g., `iconv -f <ENC> -t UTF-8 prompt.txt`)."
	case "invalid-utf16":
		return "input looked like " + e.encoding + " but could not be decoded. Convert it to UTF-8 and retry."
	case "unsupported-bom":
		return "input appears to be " + e.encoding + ". Convert it to UTF-8 and retry."
	default:
		return "input could not be decoded"
	}
}

func decodePromptBytes(input []byte) (string, error) {
	input = trimPrefixBytes(input, []byte{0xEF, 0xBB, 0xBF})
	if hasPrefixBytes(input, []byte{0xFF, 0xFE, 0x00, 0x00}) {
		return "", &promptDecodeError{kind: "unsupported-bom", encoding: "UTF-32LE"}
	}
	if hasPrefixBytes(input, []byte{0x00, 0x00, 0xFE, 0xFF}) {
		return "", &promptDecodeError{kind: "unsupported-bom", encoding: "UTF-32BE"}
	}
	if hasPrefixBytes(input, []byte{0xFF, 0xFE}) {
		return decodeUTF16(input[2:], "UTF-16LE", func(lo, hi byte) uint16 {
			return uint16(lo) | uint16(hi)<<8
		})
	}
	if hasPrefixBytes(input, []byte{0xFE, 0xFF}) {
		return decodeUTF16(input[2:], "UTF-16BE", func(hi, lo byte) uint16 {
			return uint16(hi)<<8 | uint16(lo)
		})
	}
	if !utf8.Valid(input) {
		return "", &promptDecodeError{kind: "invalid-utf8", validUpTo: validUTF8PrefixLen(input)}
	}
	return string(input), nil
}

func decodeUTF16(input []byte, encoding string, decodeUnit func(byte, byte) uint16) (string, error) {
	if len(input)%2 != 0 {
		return "", &promptDecodeError{kind: "invalid-utf16", encoding: encoding}
	}
	units := make([]uint16, 0, len(input)/2)
	for i := 0; i < len(input); i += 2 {
		units = append(units, decodeUnit(input[i], input[i+1]))
	}
	if !validUTF16(units) {
		return "", &promptDecodeError{kind: "invalid-utf16", encoding: encoding}
	}
	return string(utf16.Decode(units)), nil
}

func validUTF16(units []uint16) bool {
	for i := 0; i < len(units); i++ {
		unit := units[i]
		switch {
		case 0xD800 <= unit && unit <= 0xDBFF:
			if i+1 >= len(units) {
				return false
			}
			next := units[i+1]
			if next < 0xDC00 || next > 0xDFFF {
				return false
			}
			i++
		case 0xDC00 <= unit && unit <= 0xDFFF:
			return false
		}
	}
	return true
}

func validUTF8PrefixLen(input []byte) int {
	for i := 0; i < len(input); {
		r, size := utf8.DecodeRune(input[i:])
		if r == utf8.RuneError && size == 1 {
			return i
		}
		i += size
	}
	return len(input)
}

func hasPrefixBytes(input []byte, prefix []byte) bool {
	if len(input) < len(prefix) {
		return false
	}
	for i := range prefix {
		if input[i] != prefix[i] {
			return false
		}
	}
	return true
}

func trimPrefixBytes(input []byte, prefix []byte) []byte {
	if hasPrefixBytes(input, prefix) {
		return input[len(prefix):]
	}
	return input
}
