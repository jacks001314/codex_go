package appserver

import (
	"encoding/json"
	"math"
	"strings"

	codexctx "codex_go/context"
	"codex_go/state"
	"codex_go/utils"
)

// Rust #44281 keeps a fixed slack between the composed review request and the
// reviewer's effective input limit
// (core/src/guardian/request_budget.rs::INPUT_TOKEN_MARGIN) so backend framing
// that the host cannot measure cannot push an admitted request over the window.
const guardianInputTokenMargin = 256

// guardianDefaultMaxInputTokens mirrors Rust's DEFAULT_MAX_INPUT_TOKENS
// (guardian-context/src/budget.rs): the conservative input ceiling used when the
// model catalog has no authoritative context window.
const guardianDefaultMaxInputTokens = 128_000

// guardianInputBudgetMessage mirrors Rust's INPUT_BUDGET_MESSAGE
// (ext/guardian-reviewer/src/completion.rs). Required evidence that cannot fit
// the complete-request budget rejects the review with this exact phrase, so the
// parent tool output and the GuardianWarning agree with the Rust host.
const guardianInputBudgetMessage = "the complete action and minimum review context exceed the reviewer input budget"

// effectiveGuardianInputTokenLimit mirrors Rust's effective_input_token_limit
// followed by the fixed margin subtraction in request_budget::prepare_prompt.
// The host resolver already applies the model's effective context-window
// percentage, so only the conservative fallback and the margin remain here.
func effectiveGuardianInputTokenLimit(contextWindow int64, windowKnown bool) int64 {
	if !windowKnown || contextWindow <= 0 {
		return guardianDefaultMaxInputTokens - guardianInputTokenMargin
	}
	if contextWindow <= guardianInputTokenMargin {
		return 0
	}
	return contextWindow - guardianInputTokenMargin
}

// guardianRequestTokenEstimate mirrors Rust's estimate_request_tokens
// (core/src/guardian/request_budget.rs): every delivered input item, the
// prompt message, and the output-format metadata all count toward the
// complete request, not just the review evidence.
func guardianRequestTokenEstimate(prompt string, items []any, outputSchema any) int64 {
	total := guardianInputItemsTokenCount(items)
	if trimmed := strings.TrimSpace(prompt); trimmed != "" {
		message, err := json.Marshal(map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": trimmed},
			},
		})
		if err != nil {
			return math.MaxInt64
		}
		total += int64(utils.ApproxTokenCount(string(message)))
	}
	if outputSchema != nil {
		metadata, err := json.Marshal(map[string]any{"text": outputSchema})
		if err != nil {
			return math.MaxInt64
		}
		total += int64(utils.ApproxTokenCount(string(metadata)))
	}
	return total
}

// guardianCompleteRequestTokens mirrors Rust's existing-context floor
// `estimate_request_tokens(request).max(total_token_usage)`: the reviewer's
// most recent usage proves at least that much context is already carried, so a
// smaller estimate cannot admit more evidence than the session can hold.
func guardianCompleteRequestTokens(prompt string, items []any, outputSchema any, historyTokens int64) int64 {
	total := guardianRequestTokenEstimate(prompt, items, outputSchema)
	if historyTokens > total {
		return historyTokens
	}
	return total
}

// guardianDropOldestOptionalEvidence omits the earliest optional transcript
// entry, mirroring Rust's ComposedContext::enforce_budget, which drops
// commentary- and tool-priority evidence before shortening older history and
// never drops required evidence. It reports false once only historical and
// required evidence remains; the caller then allows bounded history shortening
// before failing closed with the input-budget message.
func guardianDropOldestOptionalEvidence(entries []state.GuardianTranscriptEntry) ([]state.GuardianTranscriptEntry, bool) {
	for index, entry := range entries {
		if !entry.Optional {
			continue
		}
		reduced := make([]state.GuardianTranscriptEntry, 0, len(entries)-1)
		reduced = append(reduced, entries[:index]...)
		reduced = append(reduced, entries[index+1:]...)
		return reduced, true
	}
	return entries, false
}

// guardianShortenOldestHistoricalEvidence mirrors Rust #44570's
// HistoryTruncation::Allow arm after optional evidence has been exhausted. It
// shortens the oldest user instruction or manual approval first, keeping that
// entry's source label and both ends around the standard truncation marker.
// Later restrictions stay complete whenever the older entry can supply the
// needed room. A shortened entry no longer proves delivery of its full retained
// source.
func guardianShortenOldestHistoricalEvidence(
	entries []state.GuardianTranscriptEntry,
	format state.GuardianTranscriptFormat,
	excess int64,
) bool {
	if excess <= 0 {
		return false
	}
	for index := range entries {
		entry := &entries[index]
		if !entry.Historical || len(entry.Native) > 0 || strings.TrimSpace(entry.Text) == "" {
			continue
		}
		original := entry.Text
		originalTokens := utils.ApproxTokenCount(entry.Render(format))
		if originalTokens <= 0 {
			continue
		}
		targetTokens := originalTokens - int(excess)
		if targetTokens < 1 {
			targetTokens = 1
		}
		upper := utils.ApproxTokenCount(original)
		lower := min(32, upper)
		if lower >= upper {
			continue
		}
		best := ""
		for low, high := lower, upper; low <= high; {
			mid := low + (high-low)/2
			candidate, _ := codexctx.GuardianTruncateText(original, mid)
			entry.Text = candidate
			if utils.ApproxTokenCount(entry.Render(format)) <= targetTokens {
				best = candidate
				low = mid + 1
			} else {
				high = mid - 1
			}
		}
		if best == "" {
			best, _ = codexctx.GuardianTruncateText(original, lower)
		}
		entry.Text = best
		if entry.Text == original {
			entry.Text = original
			continue
		}
		entry.RetainedSource = nil
		return true
	}
	return false
}

// guardianBudgetOmissionNote combines the profile's retention note with the
// aggregate-budget omission notice so the reviewer learns that both evidence
// selection stages reduced the transcript.
func guardianBudgetOmissionNote(existing string) string {
	existing = strings.TrimSpace(existing)
	if existing == "" {
		return state.GuardianBudgetOmissionNotice
	}
	if strings.Contains(existing, state.GuardianBudgetOmissionNotice) {
		return existing
	}
	return existing + "\n" + state.GuardianBudgetOmissionNotice
}
