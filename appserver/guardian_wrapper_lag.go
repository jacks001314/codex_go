package appserver

import (
	"strings"

	"codex_go/tool"
)

// maxGuardianWrapperStarts mirrors Rust's WrapperLag history bound. Missing or
// evicted provenance keeps the full score lag rather than guessing a parent.
const maxGuardianWrapperStarts = 256

type guardianWrapperStart struct {
	callID             string
	index              int
	wrapperItemID      string
	parentWrapperIndex int
}

// recordToolStart records one lifecycle start for Guardian score freshness.
// The index is the same monotonic tool-call position used by scoreLag.
func (r *modelGuardianReviewer) recordToolStart(threadID string, invocation *tool.Invocation) {
	if r == nil || invocation == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	threadID = strings.TrimSpace(threadID)
	originItemID := guardianInvocationOriginItemID(invocation)
	source := strings.TrimSpace(invocation.Source)
	r.scoreMu.Lock()
	defer r.scoreMu.Unlock()
	progress := r.ensureScoreProgressLocked(threadID)
	progress.latestToolCall++
	start := guardianWrapperStart{
		callID:        strings.TrimSpace(invocation.CallID),
		index:         progress.latestToolCall,
		wrapperItemID: "",
	}
	if source == "code_mode" && originItemID != "" {
		for index := len(progress.wrapperStarts) - 1; index >= 0; index-- {
			if progress.wrapperStarts[index].wrapperItemID == originItemID {
				start.parentWrapperIndex = progress.wrapperStarts[index].index
				break
			}
		}
	}
	// Only a direct custom `exec` call is a code-mode wrapper. Nested calls and
	// other executors advance lag without becoming discountable wrappers.
	if source == "" &&
		invocation.ToolName.Namespace == "" &&
		invocation.ToolName.Name == tool.CodeModeExecToolName &&
		invocation.Payload.Kind == tool.PayloadCustom {
		start.wrapperItemID = originItemID
	}
	progress.appendWrapperStartLocked(start)
}

// recordGuardianToolStart feeds lifecycle provenance into the active model
// reviewer. A custom reviewer owns its own lag policy and is left untouched.
func (r *RuntimeRouter) recordGuardianToolStart(threadID string, invocation *tool.Invocation) {
	if r == nil {
		return
	}
	reviewer, ok := r.services.GuardianReviewer.(*modelGuardianReviewer)
	if !ok || reviewer == nil {
		return
	}
	reviewer.recordToolStart(threadID, invocation)
}

func (r *modelGuardianReviewer) ensureScoreProgressLocked(threadID string) *guardianScoreProgress {
	if r.scoreProgress == nil {
		r.scoreProgress = map[string]*guardianScoreProgress{}
	}
	progress := r.scoreProgress[threadID]
	if progress == nil {
		progress = &guardianScoreProgress{}
		r.scoreProgress[threadID] = progress
	}
	return progress
}

func (p *guardianScoreProgress) appendWrapperStartLocked(start guardianWrapperStart) {
	if p == nil {
		return
	}
	if len(p.wrapperStarts) >= maxGuardianWrapperStarts {
		copy(p.wrapperStarts, p.wrapperStarts[1:])
		p.wrapperStarts = p.wrapperStarts[:len(p.wrapperStarts)-1]
	}
	p.wrapperStarts = append(p.wrapperStarts, start)
}

func (p *guardianScoreProgress) hasWrapperStartLocked(callID string) bool {
	if p == nil || callID == "" {
		return false
	}
	for index := len(p.wrapperStarts) - 1; index >= 0; index-- {
		if p.wrapperStarts[index].callID == callID {
			return true
		}
	}
	return false
}

func (p *guardianScoreProgress) wrapperDiscountLocked(callID string, latestScored int) int {
	if p == nil || callID == "" {
		return 0
	}
	for index := len(p.wrapperStarts) - 1; index >= 0; index-- {
		start := p.wrapperStarts[index]
		if start.callID != callID {
			continue
		}
		if start.parentWrapperIndex > latestScored {
			return 1
		}
		return 0
	}
	return 0
}

func guardianInvocationOriginItemID(invocation *tool.Invocation) string {
	if invocation == nil || invocation.Context == nil {
		return ""
	}
	originItemID, _ := invocation.Context[tool.OriginItemIDContextKey].(string)
	return strings.TrimSpace(originItemID)
}
