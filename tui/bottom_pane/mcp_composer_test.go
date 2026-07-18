package bottompane

import (
	"strings"
	"testing"
)

func TestElicitationFormBuildsFieldsAndSubmitsContent(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"required": []any{
			"email",
		},
		"properties": map[string]any{
			"email": map[string]any{
				"type":        "string",
				"title":       "Email",
				"description": "Account email",
			},
			"notify": map[string]any{
				"type":    "boolean",
				"default": true,
			},
			"region": map[string]any{
				"type": "string",
				"enum": []any{"us", "eu"},
			},
			"scopes": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
					"enum": []any{"read", "write"},
				},
			},
			"token": map[string]any{
				"type":   "string",
				"format": "password",
			},
		},
	}

	form, err := NewElicitationFormRequest("docs", "request-1", "Connect docs", schema, nil)
	if err != nil {
		t.Fatalf("NewElicitationFormRequest error = %v", err)
	}
	if form.ResponseMode != ElicitationFormContent || len(form.Fields) != 5 {
		t.Fatalf("form = %#v", form)
	}
	if _, err := form.Submit(); err == nil {
		t.Fatal("expected required email error")
	}
	if !form.SetValue("email", "team@example.test") ||
		!form.SetValue("region", "eu") ||
		!form.ToggleOption("scopes", "write") ||
		!form.SetValue("token", "secret") {
		t.Fatalf("set values failed: %#v", form.Fields)
	}
	decision, err := form.Submit()
	if err != nil {
		t.Fatalf("Submit error = %v", err)
	}
	if decision.Action != ElicitationAccept ||
		decision.Content["email"] != "team@example.test" ||
		decision.Content["region"] != "eu" ||
		decision.Content["notify"] != true {
		t.Fatalf("decision = %#v", decision)
	}
	if lines := strings.Join(form.RenderLines(80), "\n"); !strings.Contains(lines, "Connect docs") || !strings.Contains(lines, "******") {
		t.Fatalf("render lines = %s", lines)
	}
}

func TestElicitationApprovalActionMapsPersistChoices(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"decision": map[string]any{
				"type": "string",
				"anyOf": []any{
					map[string]any{"const": ApprovalAcceptOnceValue, "title": "Allow once"},
					map[string]any{"const": ApprovalAcceptSessionValue, "title": "Allow session"},
					map[string]any{"const": ApprovalDeclineValue, "title": "Decline"},
					map[string]any{"const": ApprovalCancelValue, "title": "Cancel"},
				},
			},
		},
	}
	form, err := NewElicitationFormRequest("docs", "approval-1", "Approve tool?", schema, nil)
	if err != nil {
		t.Fatalf("NewElicitationFormRequest error = %v", err)
	}
	if form.ResponseMode != ElicitationApprovalAction {
		t.Fatalf("response mode = %s", form.ResponseMode)
	}
	if !form.SetValue("decision", ApprovalAcceptSessionValue) {
		t.Fatal("set decision failed")
	}
	decision, err := form.Submit()
	if err != nil {
		t.Fatalf("Submit error = %v", err)
	}
	if decision.Action != ElicitationAccept || decision.Persist != "session" || decision.Content != nil {
		t.Fatalf("approval decision = %#v", decision)
	}
	if !form.SetValue("decision", ApprovalDeclineValue) {
		t.Fatal("set decline failed")
	}
	decision, err = form.Submit()
	if err != nil || decision.Action != ElicitationDecline {
		t.Fatalf("decline decision = %#v err=%v", decision, err)
	}
}

func TestElicitationSchemaEnumNamesAndOneOfMatchRust(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"region": map[string]any{
				"type":      "string",
				"enum":      []any{"us", "eu"},
				"enumNames": []any{"United States", "Europe"},
				"default":   "eu",
			},
			"mode": map[string]any{
				"type": "string",
				"oneOf": []any{
					map[string]any{"const": "fast", "title": "Fast path"},
					map[string]any{"const": "safe", "title": "Safe path"},
				},
				"default": "safe",
			},
		},
	}
	form, err := NewElicitationFormRequest("docs", "request-enum", "Configure docs", schema, nil)
	if err != nil {
		t.Fatalf("NewElicitationFormRequest error = %v", err)
	}
	region := form.Fields[1]
	if region.Name != "region" || len(region.Options) != 2 || region.Options[0].Label != "United States" || region.Options[1].Label != "Europe" || region.Value != "eu" {
		t.Fatalf("region field = %#v", region)
	}
	mode := form.Fields[0]
	if mode.Name != "mode" || len(mode.Options) != 2 || mode.Options[0] != (ElicitationOption{Value: "fast", Label: "Fast path"}) || mode.Options[1] != (ElicitationOption{Value: "safe", Label: "Safe path"}) || mode.Value != "safe" {
		t.Fatalf("mode field = %#v", mode)
	}
}

func TestChatComposerDraftHistoryQueueAndFooter(t *testing.T) {
	composer := NewChatComposerState()
	composer.Draft.Insert("hello")
	composer.Draft.Insert(" world")
	composer.Draft.MoveCursor(-6)
	composer.Draft.Insert(",")
	if composer.Draft.Text != "hello, world" {
		t.Fatalf("draft text = %q", composer.Draft.Text)
	}
	composer.Draft.AddAttachment(ComposerAttachment{Kind: AttachmentImage, Path: `D:\img\chart.png`})
	if composer.Draft.Attachments[0].Label() != "chart.png" {
		t.Fatalf("attachment label = %q", composer.Draft.Attachments[0].Label())
	}
	submission, ok := composer.Submit(false)
	if !ok || submission.Text != "hello, world" || len(submission.Attachments) != 1 {
		t.Fatalf("submission = %#v ok=%v", submission, ok)
	}
	if !composer.Draft.IsEmpty() {
		t.Fatalf("draft should be empty: %#v", composer.Draft)
	}
	if previous, ok := composer.History.Previous(); !ok || previous != "hello, world" {
		t.Fatalf("history previous = %q ok=%v", previous, ok)
	}

	composer.Draft.Insert("queued")
	if submission, ok := composer.Submit(true); ok || submission.Text != "" || len(composer.Queue) != 1 {
		t.Fatalf("running submit = %#v ok=%v queue=%#v", submission, ok, composer.Queue)
	}
	if next, ok := composer.Dequeue(); !ok || next.Text != "queued" {
		t.Fatalf("dequeue = %#v ok=%v", next, ok)
	}

	footer := ComposerFooterState{Running: true, QueuedCount: 2, ContextPercent: 55, ActiveAgentLabel: "agent-main", Mode: "plan"}
	line := footer.Render(120)
	for _, want := range []string{"plan", "Ctrl+C interrupt", "Ctrl+G editor", "2 queued", "context 55%", "agent-main"} {
		if !strings.Contains(line, want) {
			t.Fatalf("footer %q missing %q", line, want)
		}
	}
}

func TestSlashInputAndRuneBackspace(t *testing.T) {
	state := DetectSlashInput("/model gp", len("/model gp"))
	if !state.Active || state.Query != "model gp" || state.Start != 0 {
		t.Fatalf("slash state = %#v", state)
	}
	state = DetectSlashInput("hello /status", len("hello /status"))
	if !state.Active || state.Query != "status" {
		t.Fatalf("slash after space = %#v", state)
	}
	draft := NewComposerDraftState()
	draft.Insert("hi")
	draft.Insert("界")
	if !draft.Backspace() || draft.Text != "hi" {
		t.Fatalf("unicode backspace text=%q cursor=%d", draft.Text, draft.Cursor)
	}
}
