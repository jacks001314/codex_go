package tea

import (
	"testing"
)

// Rust #63054: shell syntax such as PowerShell's `$env:HTTP_PROXY` must not
// open completion menus while a `!` shell command is being typed.
func TestShellModeSuppressesCompletionPopupsLikeRust(t *testing.T) {
	model := newAsyncQuestionModel()
	model.composer.SetValue("!echo $env:HTTP_PROXY")
	model.composerPrefix = composerPrefixShell
	if !model.isBangShellCommand() {
		t.Fatal("model is not in shell mode")
	}
	model.refreshSlashPopup()
	model.refreshSkillPopup()
	if model.slashPopup.Active {
		t.Fatalf("slash popup active in shell mode: %#v", model.slashPopup)
	}
	if model.skillPopup.Active {
		t.Fatalf("skill popup active in shell mode: %#v", model.skillPopup)
	}
	if model.mentionPopup != nil {
		t.Fatalf("mention popup active in shell mode: %#v", model.mentionPopup)
	}

	model.composer.SetValue("!cat @README.md")
	model.refreshSkillPopup()
	if model.skillPopup.Active || model.mentionPopup != nil {
		t.Fatalf("mention completion opened in shell mode: skill=%#v mention=%#v", model.skillPopup, model.mentionPopup)
	}

	// Leaving shell mode restores ordinary completion.
	model.composerPrefix = composerPrefixNone
	model.composer.SetValue("$demo")
	model.refreshSkillPopup()
	if !model.skillPopup.Active {
		t.Fatalf("skill popup did not reopen outside shell mode: %#v", model.skillPopup)
	}
}
