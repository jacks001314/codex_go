package prompt

import (
	"errors"
	"io"
	"strings"
)

func Resolve(promptArg string, stdin io.Reader) (string, error) {
	stdinText, err := readAllString(stdin)
	if err != nil {
		return "", err
	}
	switch {
	case promptArg == "-":
		if strings.TrimSpace(stdinText) == "" {
			return "", errors.New("no prompt provided via stdin")
		}
		return stdinText, nil
	case promptArg != "":
		if strings.TrimSpace(stdinText) == "" {
			return promptArg, nil
		}
		return WithStdinContext(promptArg, stdinText), nil
	default:
		if strings.TrimSpace(stdinText) == "" {
			return "", errors.New("no prompt provided. Either specify one as an argument or pipe the prompt into stdin")
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
	return string(data), nil
}
