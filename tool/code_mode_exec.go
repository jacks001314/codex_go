package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const CodeModeExecToolName = "exec"

const codeModeExecGrammar = `start: pragma_source | plain_source
pragma_source: PRAGMA_LINE NEWLINE SOURCE
plain_source: SOURCE

PRAGMA_LINE: /[ \t]*\/\/ @exec:[^\r\n]*/
NEWLINE: /\r?\n/
SOURCE: /[\s\S]+/
`

var codeModeExecCommandPattern = regexp.MustCompile(`(?s)tools\.exec_command\s*\(\s*(\{.*?\})\s*\)`)

func NewCodeModeExecExecutor(shell *ShellExecutor) Executor {
	return NewExecutorFunc(Spec{
		Name:        PlainName(CodeModeExecToolName),
		Description: "Run JavaScript code to orchestrate/compose tool calls. Accepts raw JavaScript source text, not JSON, quoted strings, markdown, or explanatory prose. All nested tools are available on the global tools object; for example await tools.exec_command({cmd: \"pwd\"}). Use text(result.output) to return output. Execute the requested code directly and do not describe it instead.",
		Freeform:    &FreeformSpec{Syntax: "lark", Definition: codeModeExecGrammar},
	}, func(ctx context.Context, invocation *Invocation) (*Output, error) {
		if invocation == nil || invocation.Payload.Kind != PayloadCustom {
			return nil, RespondToModel("exec expects raw JavaScript source text")
		}
		matches := codeModeExecCommandPattern.FindAllStringSubmatch(invocation.Payload.Input, -1)
		if len(matches) == 0 {
			return nil, RespondToModel("exec currently requires a tools.exec_command({...}) call")
		}
		outputs := make([]string, 0, len(matches))
		commands := make([]string, 0, len(matches))
		exitCodes := make([]int, 0, len(matches))
		success := true
		for index, match := range matches {
			var args map[string]any
			if err := json.Unmarshal([]byte(match[1]), &args); err != nil {
				return nil, RespondToModel(fmt.Sprintf("exec could not decode tools.exec_command call %d: %v", index+1, err))
			}
			encoded, _ := json.Marshal(args)
			output, err := shell.Execute(ctx, &Invocation{
				CallID: fmt.Sprintf("%s-nested-%d", invocation.CallID, index+1), ToolName: PlainName(DefaultExecCommandToolName),
				Payload: Payload{Kind: PayloadFunction, Arguments: string(encoded)}, Context: invocation.Context,
			})
			if err != nil {
				return nil, err
			}
			if output != nil {
				success = success && output.Success
				outputs = append(outputs, normalizeNestedExecOutput(output.Body))
				exitCode := 0
				if value, ok := output.Data["exit_code"].(int); ok {
					exitCode = value
				}
				exitCodes = append(exitCodes, exitCode)
				if command, ok := args["cmd"].(string); ok {
					commands = append(commands, command)
				}
			}
		}
		return &Output{CallID: invocation.CallID, ToolName: PlainName(CodeModeExecToolName), Success: success, Body: strings.Join(outputs, "\n"), Data: map[string]any{"nested_commands": commands, "nested_outputs": outputs, "nested_exit_codes": exitCodes}}, nil
	})
}

func normalizeNestedExecOutput(value string) string {
	if marker := strings.Index(value, "Output:\n"); marker >= 0 {
		value = value[marker+len("Output:\n"):]
	}
	return strings.TrimSuffix(value, "\n")
}
