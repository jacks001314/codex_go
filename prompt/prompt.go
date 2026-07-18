package prompt

import (
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

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
