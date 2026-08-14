package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"codex_go/codexapi"
)

const responsesDiagnosticsEnv = "CODEX_GO_RESPONSES_DEBUG"
const responsesDiagnosticsFileEnv = "CODEX_GO_RESPONSES_DEBUG_FILE"

func responsesDiagnosticsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(responsesDiagnosticsEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func responsesDiagnosticErrorKind(err error) string {
	var apiError *codexapi.APIError
	if errors.As(err, &apiError) && apiError != nil {
		return string(apiError.Details().Kind)
	}
	var responsesError *ResponsesAPIError
	if errors.As(err, &responsesError) && responsesError != nil {
		return fmt.Sprintf("http_%d", responsesError.StatusCode)
	}
	return fmt.Sprintf("%T", err)
}

func diagnosticErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func responsesDiagnostic(event string, fields map[string]any) {
	if !responsesDiagnosticsEnabled() {
		return
	}
	record := map[string]any{"event": event, "timestamp": time.Now().UTC().Format(time.RFC3339Nano)}
	for key, value := range fields {
		record[key] = value
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		fmt.Fprintf(os.Stderr, "codex_go.responses_debug event=%s marshal_error=%q\n", event, err.Error())
		return
	}
	fmt.Fprintf(os.Stderr, "codex_go.responses_debug %s\n", encoded)
	if path := strings.TrimSpace(os.Getenv(responsesDiagnosticsFileEnv)); path != "" {
		if file, openErr := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); openErr == nil {
			_, _ = file.Write(append(encoded, '\n'))
			_ = file.Close()
		}
	}
}

// recordResponsesRetry emits the structured retry event shape introduced by
// Rust #38452. The Go diagnostic sink is opt-in (CODEX_GO_RESPONSES_DEBUG),
// mirroring Rust's `codex_otel.trace_safe` tracing target without adding a new
// mandatory telemetry provider.
func recordResponsesRetry(operation string, attempt uint64, delay time.Duration, layer string) {
	responsesDiagnostic("codex.retry", map[string]any{
		"retry.attempt":   attempt,
		"retry.delay_ms":  delay.Milliseconds(),
		"retry.layer":     layer,
		"retry.operation": operation,
	})
}

func responsesRequestDiagnosticFields(request *AgentRequest, apiRequest *responsesAgentRequest) map[string]any {
	fields := map[string]any{}
	if request != nil {
		fields["thread_id"] = request.ThreadID
		fields["turn_id"] = request.TurnID
		fields["previous_response_id_present"] = strings.TrimSpace(request.PreviousResponseID) != ""
		fields["prompt_present"] = strings.TrimSpace(request.Prompt) != ""
		fields["input_summary"] = responsesInputSummary(responsesInputItems(request))
	}
	if apiRequest != nil {
		fields["model"] = apiRequest.Model
		fields["store"] = apiRequest.Store
		fields["tool_count"] = len(apiRequest.Tools)
		if encoded, err := json.Marshal(apiRequest); err == nil {
			fields["request_body_bytes"] = len(encoded)
			fields["request_body"] = json.RawMessage(encoded)
		}
	}
	return fields
}

func responsesInputSummary(items []any) map[string]any {
	types := make([]string, 0, len(items))
	shapes := make([]map[string]any, 0, len(items))
	calls := map[string]int{}
	outputs := map[string]int{}
	for _, item := range items {
		encoded, err := json.Marshal(item)
		if err != nil {
			types = append(types, "<marshal_error>")
			continue
		}
		var value map[string]any
		if json.Unmarshal(encoded, &value) != nil {
			types = append(types, "<non_object>")
			continue
		}
		itemType := strings.TrimSpace(responseToolString(value["type"]))
		if itemType == "" {
			itemType = "<unknown>"
		}
		types = append(types, itemType)
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		shape := map[string]any{"type": itemType, "keys": keys}
		for _, key := range []string{"role", "phase", "status", "execution"} {
			if text := strings.TrimSpace(responseToolString(value[key])); text != "" {
				shape[key] = text
			}
		}
		shape["id_present"] = strings.TrimSpace(responseToolString(value["id"])) != ""
		shape["call_id_present"] = strings.TrimSpace(responseToolString(value["call_id"])) != ""
		if itemType == "reasoning" {
			shape["encrypted_content_present"] = strings.TrimSpace(responseToolString(value["encrypted_content"])) != ""
		}
		shapes = append(shapes, shape)
		callID := strings.TrimSpace(responseToolString(value["call_id"]))
		switch itemType {
		case "function_call", "custom_tool_call", "local_shell_call", "tool_search_call":
			if callID != "" {
				calls[callID]++
			}
		case "function_call_output", "custom_tool_call_output", "local_shell_call_output", "tool_search_output":
			if callID != "" {
				outputs[callID]++
			}
		}
	}
	unmatchedCalls := 0
	unmatchedOutputs := 0
	for callID, count := range calls {
		if count > outputs[callID] {
			unmatchedCalls += count - outputs[callID]
		}
	}
	for callID, count := range outputs {
		if count > calls[callID] {
			unmatchedOutputs += count - calls[callID]
		}
	}
	return map[string]any{
		"count":             len(items),
		"types":             types,
		"shapes":            shapes,
		"unmatched_calls":   unmatchedCalls,
		"unmatched_outputs": unmatchedOutputs,
	}
}
