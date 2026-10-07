package model

import (
	"reflect"
	"strings"
	"sync"
)

// InferenceToolTracker is the sampling-side contract the turn loop consults
// before every sampling request: it reports whether the full model-visible tool
// list differs from the one the previous request used (Rust #50964
// `ModelClientSession::inference_tools_changed`). The retained list survives
// turns and connection resets, so the first request of a conversation only
// establishes the baseline and never counts as a change.
type InferenceToolTracker interface {
	InferenceToolsChanged(sessionKey string, tools []any) bool
}

// responsesInferenceToolCache retains the last model-visible tool list per
// sampling session (Rust #50964 `ModelClientState::last_inference_tools`). It
// lives on the runner so shallow clones (WithStreamHandler) share the very same
// state, and it is keyed by conversation so separate threads never influence
// each other's baseline.
type responsesInferenceToolCache struct {
	mu    sync.Mutex
	tools map[string][]any
}

// InferenceToolsChanged records tools as the session's latest model-visible tool
// list and reports whether it differs from the previously recorded list. The
// previous list is retained across turns and connection resets, matching Rust's
// `ModelClientState::last_inference_tools`.
func (r *ResponsesAgentRunner) InferenceToolsChanged(sessionKey string, tools []any) bool {
	if r == nil || r.inferenceTools == nil {
		return false
	}
	key := strings.TrimSpace(sessionKey)
	r.inferenceTools.mu.Lock()
	defer r.inferenceTools.mu.Unlock()
	if r.inferenceTools.tools == nil {
		r.inferenceTools.tools = map[string][]any{}
	}
	previous, recorded := r.inferenceTools.tools[key]
	r.inferenceTools.tools[key] = cloneInferenceTools(tools)
	return recorded && !inferenceToolsEqual(previous, tools)
}

func cloneInferenceTools(tools []any) []any {
	if tools == nil {
		return nil
	}
	return append([]any(nil), tools...)
}

// inferenceToolsEqual compares two model-visible tool lists the way Rust
// compares `Arc<[ToolSpec]>`: an absent list equals an empty one, while order,
// names and every declared schema field matter.
func inferenceToolsEqual(a []any, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}
