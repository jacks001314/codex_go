package appserver

import (
	"context"
	"strings"

	"codex_go/state"
)

type guardianEnclosingInvocationKey struct{}

func withGuardianEnclosingInvocation(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, guardianEnclosingInvocationKey{}, id)
}

func guardianEnclosingInvocationFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(guardianEnclosingInvocationKey{}).(string)
	return id
}

func (s *guardianReviewTranscriptSnapshot) enclosingCall(id string) *state.GuardianEnclosingCall {
	if s == nil || id == "" {
		return nil
	}
	count := 0
	var call *state.GuardianEnclosingCall
	for _, item := range s.collected {
		if item.callID != id {
			continue
		}
		count++
		if item.callMatches != 1 || strings.TrimSpace(item.callArguments) == "" {
			return nil
		}
		if len(item.entry.Native) == 0 && strings.HasSuffix(item.entry.LineRole, " call") {
			call = &state.GuardianEnclosingCall{Tool: item.entry.LineRole, Arguments: item.callArguments, MaxBytes: s.profile.Transcript.EntryLimits.ToolTokens * 4}
		}
	}
	if count != 1 {
		return nil
	}
	return call
}
