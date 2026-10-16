package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// Rust #54385 (upstream 36803b180a) introduced
// codex-rs/tui/src/chatwidget/slash_command_args.rs: one tokenizer shared by
// `/model` and `/permissions`, with shlex quoting, `--setting=value` and
// `--setting value`, switches that never consume a value, `--no-` boolean
// settings, and a bare `--` that leaves the remaining tokens positional. These
// vectors are the Rust cases of slash_command_args_tests.rs.

func TestSlashCommandArgsParsesPositionalValuesSwitchesAndQuotingLikeRust(t *testing.T) {
	parsed, failure := parseSlashCommandArgs(
		"'team review' item --reviewer user --speed=fast --clear --no-colors -- --literal",
		[]string{"--reviewer", "--speed"},
		[]string{"--clear"},
		[]string{"colors"},
	)
	if failure != "" {
		t.Fatalf("parseSlashCommandArgs failed: %s", failure)
	}
	if got := strings.Join(parsed.positionals, "|"); got != "team review|item|--literal" {
		t.Fatalf("positionals = %q, want %q", got, "team review|item|--literal")
	}
	if value, ok := parsed.value("--reviewer"); !ok || value != "user" {
		t.Fatalf("--reviewer = %q ok=%v, want user", value, ok)
	}
	if value, ok := parsed.value("--speed"); !ok || value != "fast" {
		t.Fatalf("--speed = %q ok=%v, want fast", value, ok)
	}
	if !parsed.hasSwitch("--clear") {
		t.Fatal("--clear switch was not recorded")
	}
	if value, ok := parsed.boolean("colors"); !ok || value {
		t.Fatalf("boolean(colors) = %v ok=%v, want false", value, ok)
	}
}

func TestSlashCommandArgsRejectsInvalidNamedArgumentsLikeRust(t *testing.T) {
	cases := []struct {
		args    string
		message string
	}{
		{"--reviewer", "Command setting `--reviewer` requires a value."},
		{"--clear=yes", "Command setting `--clear` does not take a value."},
		{"--clear --clear", "Command setting `--clear` was provided more than once."},
		{"--colors --no-colors", "Specify only one of --colors or --no-colors."},
		{"--unknown", "Unknown command setting `--unknown`."},
		{"'unfinished", "Invalid command arguments. Check the quoting."},
	}
	for _, testCase := range cases {
		if _, failure := parseSlashCommandArgs(testCase.args, []string{"--reviewer"}, []string{"--clear"}, []string{"colors"}); failure != testCase.message {
			t.Fatalf("parseSlashCommandArgs(%q) failure = %q, want %q", testCase.args, failure, testCase.message)
		}
	}
}

// Batch T2-8b: the shared inline-argument list now marks Pwd and Voice like Rust
// (codex-rs/tui/src/slash_command.rs:189 and :183), so `/pwd extra` and
// `/voice nope` dispatch locally instead of being sent to the model as a prompt,
// and they reach the commands' own argument handling (Rust
// chatwidget/slash_dispatch.rs:846-848 and :861-872).
func TestPwdAndVoiceArgsDispatchLocallyLikeRust(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	model := NewModel(state, Options{
		Width:  90,
		Height: 24,
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			t.Fatalf("inline-argument command was submitted as a prompt: %#v", request)
			return nil
		},
	})

	typeText(t, model, "/pwd extra")
	model.Update(key(bubbletea.KeyEnter))
	if want := "Usage: /pwd"; model.notice != want {
		t.Fatalf("/pwd extra notice = %q, want %q", model.notice, want)
	}

	typeText(t, model, "/voice nope")
	model.Update(key(bubbletea.KeyEnter))
	if notice := model.notice; !strings.Contains(notice, "Usage: /voice") {
		t.Fatalf("/voice nope notice = %q, want the usage text", notice)
	}
}
