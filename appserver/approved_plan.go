package appserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"codex_go/session"
	"codex_go/turn"
)

const (
	// approvedPlanFallbackTokens mirrors Rust's MAX_FALLBACK_TOKENS.
	approvedPlanFallbackTokens = 10_000
	// approvedPlanFallbackItems mirrors Rust's MAX_FALLBACK_ITEMS.
	approvedPlanFallbackItemLimit = 32
	// approvedPlanBytesPerToken is the harness' approximate byte budget.
	approvedPlanBytesPerToken = 4
)

var (
	errApprovedPlanEmpty   = errors.New("The approved plan is empty.")
	errApprovedPlanTooBig  = errors.New("The executor could not read the plan attachment and the complete plan is too large to send as context. Implement it in the original thread.")
	errApprovedPlanPathBad = errors.New("`attachmentPath` must be absolute on the app server")
)

// handleThreadPrepareApprovedPlanRuntime mirrors Rust #60763
// (thread/prepareApprovedPlan): prepare the first turn of a fresh thread for a
// plan approved in another thread.
func (r *RuntimeRouter) handleThreadPrepareApprovedPlanRuntime(request *Request) (*ThreadPrepareApprovedPlanResponse, error) {
	var params ThreadPrepareApprovedPlanParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	threadID := strings.TrimSpace(params.ThreadID)
	if record, err := r.threadRecord(session.ThreadID(threadID), true, false); err != nil || record == nil {
		return nil, jsonRPCInvalidRequest("thread not found: " + threadID)
	}
	if err := r.ensureDirectInputAllowed(request, threadID); err != nil {
		return nil, err
	}
	if r.threads != nil && r.threads.ActiveTurn(threadID) != nil {
		return nil, jsonRPCInvalidRequest("The thread already has an active turn.")
	}
	if actual := utf8.RuneCountInString(params.Markdown); actual > turn.MaxUserInputTextChars {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("input is too large: %d characters (max %d)", actual, turn.MaxUserInputTextChars))
	}
	prompt, items, err := prepareApprovedPlanPayload(params.Markdown, params.AttachmentPath)
	if err != nil {
		return nil, jsonRPCInvalidRequest(err.Error())
	}
	if len(items) > 0 {
		rawItems := make([]json.RawMessage, 0, len(items))
		for _, item := range items {
			encoded, err := json.Marshal(item)
			if err != nil {
				return nil, err
			}
			rawItems = append(rawItems, encoded)
		}
		injectParams, err := json.Marshal(ThreadInjectItemsParams{ThreadID: threadID, Items: rawItems})
		if err != nil {
			return nil, err
		}
		if _, err := r.handleThreadInjectItemsRuntime(&Request{
			Method:   MethodThreadInjectItems,
			Params:   injectParams,
			Internal: request.Internal,
		}); err != nil {
			return nil, err
		}
	}
	return &ThreadPrepareApprovedPlanResponse{Prompt: prompt}, nil
}

// prepareApprovedPlanPayload decides whether the attachment can be referenced
// verbatim or whether the plan itself has to travel as untrusted context.
func prepareApprovedPlanPayload(markdown string, attachmentPath *string) (string, []map[string]any, error) {
	if strings.TrimSpace(markdown) == "" {
		return "", nil, errApprovedPlanEmpty
	}
	if attachmentPath != nil && strings.TrimSpace(*attachmentPath) != "" {
		path := strings.TrimSpace(*attachmentPath)
		if !filepath.IsAbs(path) {
			return "", nil, errApprovedPlanPathBad
		}
		if native, ok := readableApprovedPlanAttachment(path, markdown); ok {
			return fmt.Sprintf("Read the approved plan at %s and implement it.", native), nil, nil
		}
	}
	items, err := approvedPlanFallbackItems(markdown)
	if err != nil {
		return "", nil, err
	}
	return "Implement the plan.", items, nil
}

// readableApprovedPlanAttachment reports whether path is a regular, non-symlink
// file whose bytes are exactly markdown (Rust's readable_path).
func readableApprovedPlanAttachment(path string, markdown string) (string, bool) {
	for _, r := range path {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(markdown)) {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != markdown {
		return "", false
	}
	return path, true
}

// approvedPlanFallbackItems chunks the plan into the untrusted
// `agent.proposed_plan` outputs Rust injects when the executor cannot read the
// attachment.
func approvedPlanFallbackItems(markdown string) ([]map[string]any, error) {
	budget := approvedPlanFallbackTokens * approvedPlanBytesPerToken
	if len(markdown) > budget || budget <= 0 {
		return nil, errApprovedPlanTooBig
	}
	items := make([]map[string]any, 0, 4)
	rest := markdown
	for len(rest) > 0 {
		end := budget
		if end > len(rest) {
			end = len(rest)
		}
		for end > 0 && !utf8.RuneStart(rest[end-1]) {
			end--
		}
		if end <= 0 || len(items) == approvedPlanFallbackItemLimit {
			return nil, errApprovedPlanTooBig
		}
		items = append(items, map[string]any{
			"type":      "function_call_output",
			"namespace": "agent",
			"name":      "proposed_plan",
			"output":    rest[:end],
		})
		rest = rest[end:]
	}
	return items, nil
}
