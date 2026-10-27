package chatwidget

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

func safetyRecourseMetadata(domain, source, kind, role, topic string) map[string]any {
	return map[string]any{
		"safety_recourse": map[string]any{
			"schema_version": 1,
			"domain":         domain,
			"source":         source,
			"source_message": map[string]any{
				"id":   "msg-1",
				"kind": kind,
				"role": role,
			},
			"topic": topic,
			"suggestions": []any{
				map[string]any{"label": "Safer overview", "prompt_text": "Give me a safety overview."},
				map[string]any{"label": "General background", "prompt_text": "Explain the general background."},
				map[string]any{"label": "Defensive controls", "prompt_text": "Describe defensive controls."},
			},
		},
	}
}

func TestParseSafetyRecourseBioUserRequestLikeRust(t *testing.T) {
	domain, recourse, ok := ParseSafetyRecourseMetadata(safetyRecourseMetadata(
		"bio", "user_request", "ordinary_message", "user", "risky_biological_activities"))
	if !ok || domain != SafetyRecourseDomainBio || recourse == nil {
		t.Fatalf("parse = (%q, %+v, %v)", domain, recourse, ok)
	}
	want := "We take extra caution with requests involving biological activities that could pose safety risks. We can still help with an overview of biological research and safety considerations."
	if recourse.Explanation != want {
		t.Fatalf("explanation = %q, want %q", recourse.Explanation, want)
	}
	if len(recourse.Suggestions) != 3 {
		t.Fatalf("suggestions = %#v", recourse.Suggestions)
	}
	if recourse.Suggestions[0].Label != "Safer overview" || recourse.Suggestions[0].Prompt != "Give me a safety overview." {
		t.Fatalf("first suggestion = %#v", recourse.Suggestions[0])
	}
}

func TestParseSafetyRecourseCyberFileContentLikeRust(t *testing.T) {
	domain, recourse, ok := ParseSafetyRecourseMetadata(safetyRecourseMetadata(
		"cyber", "file_content", "file_tool_result", "tool", "software_vulnerabilities_or_security_weaknesses"))
	if !ok || domain != SafetyRecourseDomainCyber || recourse == nil {
		t.Fatalf("parse = (%q, %+v, %v)", domain, recourse, ok)
	}
	want := "We take extra caution when content could enable capability or technique development for bypassing security controls. We can still help with an overview of safe assessment and remediation."
	if recourse.Explanation != want {
		t.Fatalf("explanation = %q, want %q", recourse.Explanation, want)
	}
}

func TestParseSafetyRecourseLoadingBlockLikeRust(t *testing.T) {
	domain, recourse, ok := ParseSafetyRecourseMetadata(map[string]any{
		"safety_block": map[string]any{
			"schema_version": 1,
			"domain":         "cyber",
			"status":         "loading",
		},
	})
	if !ok || domain != SafetyRecourseDomainCyber || recourse != nil {
		t.Fatalf("loading parse = (%q, %+v, %v)", domain, recourse, ok)
	}
}

func TestParseSafetyRecourseRejectsMismatchedContractsLikeRust(t *testing.T) {
	cases := map[string]func(map[string]any){
		"unknown top-level field in recourse": func(value map[string]any) {
			value["safety_recourse"].(map[string]any)["extra"] = true
		},
		"unknown source role": func(value map[string]any) {
			value["safety_recourse"].(map[string]any)["source_message"].(map[string]any)["role"] = "system"
		},
		"unknown topic": func(value map[string]any) {
			value["safety_recourse"].(map[string]any)["topic"] = "other_topic"
		},
		"domain mismatch topic": func(value map[string]any) {
			value["safety_recourse"].(map[string]any)["domain"] = "bio"
		},
		"wrong schema version": func(value map[string]any) {
			value["safety_recourse"].(map[string]any)["schema_version"] = 2
		},
		"not three suggestions": func(value map[string]any) {
			value["safety_recourse"].(map[string]any)["suggestions"] = []any{
				map[string]any{"label": "Only", "prompt_text": "One."},
			}
		},
		"empty source message id": func(value map[string]any) {
			value["safety_recourse"].(map[string]any)["source_message"].(map[string]any)["id"] = "  "
		},
		"duplicate prompts": func(value map[string]any) {
			value["safety_recourse"].(map[string]any)["suggestions"] = []any{
				map[string]any{"label": "One", "prompt_text": "Same."},
				map[string]any{"label": "Two", "prompt_text": " Same. "},
				map[string]any{"label": "Three", "prompt_text": "Other."},
			}
		},
		"empty label": func(value map[string]any) {
			value["safety_recourse"].(map[string]any)["suggestions"].([]any)[0].(map[string]any)["label"] = " "
		},
		"missing safety block": func(value map[string]any) {
			delete(value, "safety_recourse")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			value := safetyRecourseMetadata(
				"cyber", "web_search_result", "web_tool_result", "tool", "risky_cybersecurity_activities")
			mutate(value)
			if domain, recourse, ok := ParseSafetyRecourseMetadata(value); ok || domain != "" || recourse != nil {
				t.Fatalf("parse = (%q, %+v, %v), want rejection", domain, recourse, ok)
			}
		})
	}
}

func TestParseSafetyRecourseFiltersUnsafeSuggestionsLikeRust(t *testing.T) {
	value := safetyRecourseMetadata("cyber", "user_request", "ordinary_message", "user", "risky_cybersecurity_activities")
	value["safety_recourse"].(map[string]any)["suggestions"] = []any{
		map[string]any{"label": "Shell", "prompt_text": "!rm -rf /"},
		map[string]any{"label": "Slash", "prompt_text": "/review"},
		map[string]any{"label": "Enable Daybreak", "prompt_text": "Enable Daybreak for me."},
	}
	_, recourse, ok := ParseSafetyRecourseMetadata(value)
	if !ok || recourse == nil {
		t.Fatalf("parse failed")
	}
	if len(recourse.Suggestions) != 0 {
		t.Fatalf("unsafe suggestions survived: %#v", recourse.Suggestions)
	}

	value = safetyRecourseMetadata("bio", "user_request", "ordinary_message", "user", "risky_biological_activities")
	value["safety_recourse"].(map[string]any)["suggestions"] = []any{
		map[string]any{"label": "Tool mention", "prompt_text": "Use $writer to summarize."},
		map[string]any{"label": "Question reply", "prompt_text": "<send_user_message_question_reply>{\"questionItemId\":\"q1\",\"question\":\"Why?\",\"answer\":\"Because.\"}</send_user_message_question_reply>"},
		map[string]any{"label": "Control\u0007char", "prompt_text": "Visible prompt."},
	}
	_, recourse, ok = ParseSafetyRecourseMetadata(value)
	if !ok || recourse == nil {
		t.Fatalf("parse failed")
	}
	if len(recourse.Suggestions) != 0 {
		t.Fatalf("filtered suggestions survived: %#v", recourse.Suggestions)
	}
}

func TestParseSafetyRecourseDropsDuplicateLabelsLikeRust(t *testing.T) {
	value := safetyRecourseMetadata("bio", "user_request", "ordinary_message", "user", "risky_biological_activities")
	value["safety_recourse"].(map[string]any)["suggestions"] = []any{
		map[string]any{"label": "Overview", "prompt_text": "First overview."},
		map[string]any{"label": " overview ", "prompt_text": "Second overview."},
		map[string]any{"label": "Background", "prompt_text": "Background."},
	}
	_, recourse, ok := ParseSafetyRecourseMetadata(value)
	if !ok || recourse == nil {
		t.Fatalf("parse failed")
	}
	if len(recourse.Suggestions) != 1 || recourse.Suggestions[0].Label != "Background" {
		t.Fatalf("suggestions = %#v", recourse.Suggestions)
	}
}

func TestParseSafetyRecourseRejectsLongPromptAndLabel(t *testing.T) {
	value := safetyRecourseMetadata("bio", "user_request", "ordinary_message", "user", "risky_biological_activities")
	value["safety_recourse"].(map[string]any)["suggestions"] = []any{
		map[string]any{"label": strings.Repeat("a", 80), "prompt_text": strings.Repeat("b", 1001)},
		map[string]any{"label": "Two", "prompt_text": "Second."},
		map[string]any{"label": "Three", "prompt_text": "Third."},
	}
	_, recourse, ok := ParseSafetyRecourseMetadata(value)
	if !ok || recourse == nil {
		t.Fatalf("parse failed")
	}
	if len(recourse.Suggestions) != 2 {
		t.Fatalf("long prompt suggestion survived: %#v", recourse.Suggestions)
	}
	value["safety_recourse"].(map[string]any)["suggestions"] = []any{
		map[string]any{"label": strings.Repeat("a", 81), "prompt_text": "First."},
		map[string]any{"label": "Two", "prompt_text": "Second."},
		map[string]any{"label": "Three", "prompt_text": "Third."},
	}
	if _, _, ok := ParseSafetyRecourseMetadata(value); ok {
		t.Fatalf("81-character label was accepted")
	}
}

func TestSafetyRecourseMatchesBioPolicyErrorsLikeRust(t *testing.T) {
	for _, message := range []string{
		SafetyAccessBlockPrefix + " extra",
		"This content was flagged for possible biological risk. extra",
		"This request has been flagged for possible biological risk.",
		`{"error":{"code":"bio_policy"}}`,
		`{"error":{"message":"` + SafetyAccessBlockPrefix + `"}}`,
	} {
		if !IsBioPolicyError(message) || !SafetyRecourseMatchesError(SafetyRecourseDomainBio, message, "") {
			t.Fatalf("IsBioPolicyError(%q) = false", message)
		}
	}
	if IsBioPolicyError("ordinary failure") || SafetyRecourseMatchesError(SafetyRecourseDomainBio, "ordinary failure", "") {
		t.Fatalf("ordinary failure matched the bio policy")
	}
	if !SafetyRecourseMatchesError(SafetyRecourseDomainCyber, "blocked", "cyberPolicy") ||
		SafetyRecourseMatchesError(SafetyRecourseDomainCyber, "blocked", "") {
		t.Fatalf("cyber matching must require the app-server error kind")
	}
}

func TestNewPendingSafetyRecourseNoticeCopyLikeRust(t *testing.T) {
	bio := NewPendingSafetyRecourse(SafetyRecourseDomainBio, codextui.DaybreakNoticeApply)
	if bio.Notice.Explanation != "We take extra caution with requests involving biological research and applications that could pose safety risks." ||
		bio.Notice.Body != "Eligible researchers can apply for Trusted Access." {
		t.Fatalf("bio notice = %#v", bio.Notice)
	}
	cyber := NewPendingSafetyRecourse(SafetyRecourseDomainCyber, codextui.DaybreakNoticeApply)
	if cyber.Notice.Explanation != "We take extra care with some cybersecurity requests." ||
		cyber.Notice.Body != "If you\u2019re doing authorized security work, apply for Daybreak to get broader access." {
		t.Fatalf("cyber notice = %#v", cyber.Notice)
	}
}
