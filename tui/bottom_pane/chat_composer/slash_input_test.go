package chatcomposer

import (
	"reflect"
	"testing"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	"codex_go/turn"
)

func slashInputForTest() SlashInput {
	return NewSlashInput(true, false, bottompane.BuiltinCommandFlags{
		CollaborationModesEnabled:   true,
		ConnectorsEnabled:           true,
		PluginsCommandEnabled:       true,
		TokenActivityCommandEnabled: true,
		ServiceTierCommandsEnabled:  true,
		GoalCommandEnabled:          true,
		PersonalityCommandEnabled:   true,
		AllowElevateSandbox:         true,
	}, []bottompane.ServiceTierCommand{{
		ID:          "fast",
		Name:        "fast",
		Description: "1.5x speed",
	}})
}

func TestSlashInputSubmissionBareInlineAndDequeueMatchRustCore(t *testing.T) {
	slash := slashInputForTest()

	if got := slash.ValidateSubmission("/model", false); !got.Valid() {
		t.Fatalf("/model validation = %#v", got)
	}
	if got := slash.ValidateSubmission("/does-not-exist", false); got.Kind != SubmissionValidationUnknownCommand || got.UnknownCommand != "does-not-exist" {
		t.Fatalf("unknown validation = %#v", got)
	}
	if got := slash.ValidateSubmission("/foo/bar", false); !got.Valid() {
		t.Fatalf("nested slash names validate as plain input, got %#v", got)
	}
	if got := slash.ValidateSubmission("/does-not-exist", true); !got.Valid() {
		t.Fatalf("leading-space submission validates as plain input, got %#v", got)
	}

	if command, ok := slash.BareCommand("/model"); !ok || command.CommandText() != "model" {
		t.Fatalf("bare command = %#v ok=%v", command, ok)
	}
	if _, ok := slash.BareCommand("/review please"); ok {
		t.Fatalf("inline args should not be treated as a bare command")
	}
	if _, ok := slash.BareCommand("/review\nplease"); ok {
		t.Fatalf("full-text inline rest should block bare inline-arg commands")
	}
	inline, ok := slash.InlineCommand("/review please inspect")
	if !ok || inline.Command.Command != codextui.CommandReview || inline.Rest != "please inspect" || inline.RestOffset != len("/review ") {
		t.Fatalf("inline command = %#v ok=%v", inline, ok)
	}
	if _, ok := slash.InlineCommand(" /review please"); ok {
		t.Fatalf("leading-space inline command should stay plain input")
	}

	if !slash.ShouldParseOnDequeue("/model") || slash.ShouldParseOnDequeue(" /model") || !slash.ShouldParseOnDequeue("\t/model") {
		t.Fatalf("ShouldParseOnDequeue did not match Rust trim/leading-space behavior")
	}
	if IsSlashInput(" /model") || !IsSlashInput("\n/model") {
		t.Fatalf("IsSlashInput compatibility helper should mirror dequeue slash detection")
	}
}

func TestSlashInputCommandElementEditingPopupAndCompletionMatchRustCore(t *testing.T) {
	slash := slashInputForTest()

	rng, ok := slash.CommandElementRange("/model x", len("/model"))
	if !ok || rng != (TextRange{Start: 0, End: len("/model")}) {
		t.Fatalf("command element range = %#v ok=%v", rng, ok)
	}
	if _, ok := slash.CommandElementRange("/model", len("/model")); ok {
		t.Fatalf("command without trailing whitespace should not become an element")
	}
	if _, ok := slash.CommandElementRange("/model x", len("/mo")); ok {
		t.Fatalf("cursor inside command name should keep it editable")
	}
	if slash.IsEditingCommandName("/model args", len("/model ")) {
		t.Fatalf("cursor after command name should not be editing the command")
	}
	if !slash.IsEditingCommandName("/mo", len("/mo")) || !slash.IsEditingCommandName("/", len("/")) {
		t.Fatalf("command prefix and empty slash should be editable")
	}

	filter, ok := CommandPopupFilterText("/model args", len("/mo"))
	if !ok || filter != "/mo" {
		t.Fatalf("popup filter = %q ok=%v", filter, ok)
	}
	if _, ok := CommandPopupFilterText("/model args", len("/model ")); ok {
		t.Fatalf("cursor outside command should not produce popup filter")
	}
	popup := slash.CommandPopup("/mo")
	selected, ok := popup.SelectedItem()
	if !ok || selected.Name != "model" {
		t.Fatalf("popup selected = %#v ok=%v", selected, ok)
	}

	model, ok := bottompane.FindSlashCommand("model", slash.CommandFlags, slash.ServiceTierCommands)
	if !ok {
		t.Fatalf("missing model command")
	}
	if completion, ok := SelectedCommandCompletion("/mo", model); !ok || completion != "/model " {
		t.Fatalf("completion = %q ok=%v", completion, ok)
	}
	if completion, ok := SelectedCommandCompletion("/model", model); ok || completion != "" {
		t.Fatalf("completed command should not produce text, got %q ok=%v", completion, ok)
	}
	skills, ok := bottompane.FindSlashCommand("skills", slash.CommandFlags, slash.ServiceTierCommands)
	if !ok || !SelectedCommandDispatchesImmediatelyOnTab(skills) {
		t.Fatalf("skills command should dispatch immediately on tab")
	}
}

func TestSlashInputPreparedArgsQueuedActionAndElementsMatchRustCore(t *testing.T) {
	rest, offset, ok := PreparedArgs("/review inspect @file")
	if !ok || rest != "inspect @file" || offset != len("/review ") {
		t.Fatalf("prepared args = rest %q offset %d ok=%v", rest, offset, ok)
	}
	if got := QueuedInputActionFor("/review now", true); got != QueuedInputParseSlash {
		t.Fatalf("slash queued action = %s", got)
	}
	if got := QueuedInputActionFor("/review now", false); got != QueuedInputPlain {
		t.Fatalf("non-deferred slash queued action = %s", got)
	}
	if got := QueuedInputActionFor("!ls", true); got != QueuedInputRunShell {
		t.Fatalf("shell queued action = %s", got)
	}

	first := "inspect"
	second := "@file"
	placeholder := "mention"
	elements := []turn.TextElement{
		{ByteRange: turn.ByteRange{Start: 0, End: 7}},
		{ByteRange: turn.ByteRange{Start: uint(offset), End: uint(offset + len(first))}},
		{ByteRange: turn.ByteRange{Start: uint(offset + len(first) + 1), End: uint(offset + len(first) + 1 + len(second) + 20)}, Placeholder: &placeholder},
	}
	got := ArgsElements(rest, offset, elements)
	want := []turn.TextElement{
		{ByteRange: turn.ByteRange{Start: 0, End: uint(len(first))}},
		{ByteRange: turn.ByteRange{Start: uint(len(first) + 1), End: uint(len(rest))}, Placeholder: &placeholder},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ArgsElements = %#v, want %#v", got, want)
	}
	if got := ArgsElements("", offset, elements); got != nil {
		t.Fatalf("empty rest should produce nil elements, got %#v", got)
	}
}
