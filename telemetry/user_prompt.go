package telemetry

import (
	"context"
	"strconv"
	"unicode/utf8"
)

// Rust parity: codex-otel's SessionTelemetry::user_prompt.

// User prompt input kinds mirror the UserInput variants the record counts.
const (
	UserPromptText       = "text"
	UserPromptImage      = "image"
	UserPromptLocalImage = "local_image"
)

// UserPromptInput is one accepted user input item.
type UserPromptInput struct {
	Kind string
	Text string
}

// EmitUserPrompt mirrors SessionTelemetry::user_prompt: the log-only record
// carries the prompt text (or `[REDACTED]` unless the session logs user
// prompts) and its rune length, and the trace-safe record carries the input
// counts, so the trace never sees the prompt itself.
func EmitUserPrompt(ctx context.Context, telemetry *SessionTelemetry, inputs []UserPromptInput) {
	if telemetry == nil {
		return
	}
	prompt := ""
	textInputs := 0
	imageInputs := 0
	localImageInputs := 0
	for _, input := range inputs {
		switch input.Kind {
		case UserPromptText:
			textInputs++
			prompt += input.Text
		case UserPromptImage:
			imageInputs++
		case UserPromptLocalImage:
			localImageInputs++
		}
	}
	promptToLog := "[REDACTED]"
	if telemetry.Metadata.LogUserPrompts {
		promptToLog = prompt
	}
	telemetry.LogAndTraceEvent(ctx, "codex.user_prompt",
		map[string]string{"prompt_length": strconv.Itoa(utf8.RuneCountInString(prompt))},
		map[string]string{"prompt": promptToLog},
		map[string]string{
			"text_input_count":        strconv.Itoa(textInputs),
			"image_input_count":       strconv.Itoa(imageInputs),
			"local_image_input_count": strconv.Itoa(localImageInputs),
		})
}
