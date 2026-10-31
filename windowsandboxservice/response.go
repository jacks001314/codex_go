package windowsandboxservice

import "strings"

const (
	// MaxResponseMessageBytes bounds the diagnostic carried by an Error response.
	MaxResponseMessageBytes = 512
)

// ErrorMessage renders an error as one bounded, printable string (Rust
// ipc::response_error_message over format!("{error:#}")). Go's fmt wrapping
// already embeds each cause in the message, so Error() carries the chain.
func ErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	var message strings.Builder
	for _, character := range err.Error() {
		if character < 0x20 || character == 0x7f {
			character = ' '
		}
		if message.Len()+len(string(character)) > MaxResponseMessageBytes {
			break
		}
		message.WriteRune(character)
	}
	return message.String()
}
