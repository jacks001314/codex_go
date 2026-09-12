package tool

import (
	"context"
	"fmt"
	"strings"
)

// DefaultRequestUserInputAsyncToolName is the structured async-question tool
// added by Rust #42178: the model asks one or more self-contained questions
// (each with optional suggested answers) and the tool returns immediately.
const DefaultRequestUserInputAsyncToolName = "request_user_input_async"

// defaultRequestUserInputAsyncDescription is the built-in tool description used
// when the model catalog does not supply a model-owned description.
const defaultRequestUserInputAsyncDescription = "Ask the user one or more questions during ongoing work. Use this tool only to request missing information, preferences, constraints, clarification, or approval. The tool returns immediately without ending the turn or waiting for a reply; any reply arrives asynchronously as a new user message. Keep questions concise, self-contained, and easy to understand, using a level of detail appropriate to the user and task. The UI always allows a free-text answer, including when suggested options are provided. A preselected option is not submitted automatically."

const requestUserInputAsyncOptionsDescription = "Suggested answers, in display order. Put the recommended answer first; the first option is preselected by default. The user can select one option or enter a free-text answer. Do not include an Other option or a free-text placeholder; the UI provides free-text input automatically. Omit options for a free-text-only question."

// RequestUserInputAsyncQuestion is one structured async question. Options is
// nil for a free-text-only question; a present-but-empty list is invalid.
type RequestUserInputAsyncQuestion struct {
	Title   string   `json:"title"`
	Options []string `json:"options,omitempty"`
}

// RequestUserInputAsyncHandler mirrors Rust
// core/src/tools/handlers/request_user_input_async.rs (#42178). It validates
// the model-authored questions, emits an async agent message carrying them, and
// returns immediately.
type RequestUserInputAsyncHandler struct {
	// Description overrides the built-in tool description from the model
	// catalog. A non-nil value (including an empty string) replaces it.
	Description *string
	// EmitAsyncQuestions, when set, lets the app-server runtime emit the
	// structured message item. The tool surface stays testable without it.
	EmitAsyncQuestions func(message string, questions []RequestUserInputAsyncQuestion)
}

type requestUserInputAsyncArgs struct {
	Questions []RequestUserInputAsyncQuestion `json:"questions"`
}

func (h *RequestUserInputAsyncHandler) Spec() Spec {
	return Spec{
		Name:        PlainName(DefaultRequestUserInputAsyncToolName),
		Description: requestUserInputAsyncDescription(h.Description),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"questions": map[string]any{
					"type":        "array",
					"minItems":    1,
					"description": "One or more self-contained questions to present together, in display order.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"title": map[string]any{
								"type":        "string",
								"description": "The complete question shown to the user, including any context needed to answer it.",
							},
							"options": map[string]any{
								"type":        "array",
								"minItems":    1,
								"description": requestUserInputAsyncOptionsDescription,
								"items":       map[string]any{"type": "string"},
							},
						},
						"required":             []string{"title"},
						"additionalProperties": false,
					},
				},
			},
			"required":             []string{"questions"},
			"additionalProperties": false,
		},
		Exposure: ExposureDirectModelOnly,
	}
}

func requestUserInputAsyncDescription(override *string) string {
	if override != nil {
		return *override
	}
	return defaultRequestUserInputAsyncDescription
}

func (h *RequestUserInputAsyncHandler) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	_ = ctx
	if invocation == nil {
		return nil, fmt.Errorf("%s handler received unsupported payload", DefaultRequestUserInputAsyncToolName)
	}
	var args requestUserInputAsyncArgs
	if err := invocation.DecodeArguments(&args); err != nil {
		return nil, err
	}
	message, err := requestUserInputAsyncMessage(args.Questions)
	if err != nil {
		return nil, err
	}
	if h.EmitAsyncQuestions != nil {
		h.EmitAsyncQuestions(message, args.Questions)
	}
	return &Output{
		Success: true,
		Body:    `{"accepted":true}`,
		Data: map[string]any{
			"accepted": true,
			"async_questions": map[string]any{
				"message":   message,
				"questions": requestUserInputAsyncQuestionsJSON(args.Questions),
				"delivery":  "async",
			},
		},
	}, nil
}

// requestUserInputAsyncMessage validates the questions and renders the
// user-visible message (title plus "- option" lines, questions separated by a
// blank line), matching Rust's handler.
func requestUserInputAsyncMessage(questions []RequestUserInputAsyncQuestion) (string, error) {
	if len(questions) == 0 {
		return "", RespondToModel("questions must not be empty")
	}
	messages := make([]string, 0, len(questions))
	for _, question := range questions {
		if strings.TrimSpace(question.Title) == "" {
			return "", RespondToModel("question titles must not be empty")
		}
		lines := []string{question.Title}
		if question.Options != nil {
			if len(question.Options) == 0 {
				return "", RespondToModel("options must contain at least one non-empty answer")
			}
			for _, option := range question.Options {
				if strings.TrimSpace(option) == "" {
					return "", RespondToModel("options must contain at least one non-empty answer")
				}
				lines = append(lines, "- "+option)
			}
		}
		messages = append(messages, strings.Join(lines, "\n"))
	}
	return strings.Join(messages, "\n\n"), nil
}

// requestUserInputAsyncQuestionsJSON renders the questions for the emitted
// item's metadata, preserving Rust's camelCase wire shape.
func requestUserInputAsyncQuestionsJSON(questions []RequestUserInputAsyncQuestion) []any {
	out := make([]any, 0, len(questions))
	for _, question := range questions {
		entry := map[string]any{"title": question.Title}
		if question.Options != nil {
			options := make([]any, 0, len(question.Options))
			for _, option := range question.Options {
				options = append(options, option)
			}
			entry["options"] = options
		}
		out = append(out, entry)
	}
	return out
}
