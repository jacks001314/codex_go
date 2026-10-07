package chatwidget

import (
	"testing"

	"codex_go/appserver"
)

// Rust #51482 `Use PathUri for skill identity and path matching`: linked skill
// mentions select a skill by parsed path identity, so equivalent Windows
// spellings (ASCII case, separators) denote one skill while POSIX paths keep
// their case. Counterpart Rust test:
// `collect_explicit_skill_mentions_matches_windows_path_identity`
// (codex-rs/skills/src/selection_tests.rs).
func TestFindSkillMentionsMatchesWindowsPathIdentityLikeRust(t *testing.T) {
	// The mention's name deliberately differs from the skill name, so only the
	// linked path can select it.
	windows := []appserver.SkillsListEntry{
		{Name: "foo-skill", Path: `C:\Skills\Foo\SKILL.md`, Enabled: true},
	}
	mentions := CollectToolMentions(`[$foo](skill://c:/skills/foo/SKILL.md)`, nil)
	got := FindSkillMentions(mentions, windows)
	if len(got) != 1 || got[0].Name != "foo-skill" {
		t.Fatalf("windows path identity match = %#v, want foo-skill", got)
	}

	// A backslash spelling is the same skill too.
	backslashes := CollectToolMentions(`[$foo](skill://C:\Skills\Foo\SKILL.md)`, nil)
	if got := FindSkillMentions(backslashes, windows); len(got) != 1 {
		t.Fatalf("native windows spelling match = %#v, want foo-skill", got)
	}

	// Outside Windows, case stays significant, so these are different files.
	posix := []appserver.SkillsListEntry{
		{Name: "foo-skill", Path: "/tmp/Skills/foo/SKILL.md", Enabled: true},
	}
	posixMentions := CollectToolMentions(`[$foo](skill:///tmp/skills/foo/SKILL.md)`, nil)
	if got := FindSkillMentions(posixMentions, posix); len(got) != 0 {
		t.Fatalf("posix case-folded match = %#v, want none", got)
	}
}

// Rust #51482 keeps literal spaces, `%` and `#` significant in native
// filenames: only the literal spelling selects the skill. Counterpart Rust test:
// `collect_explicit_skill_mentions_preserves_native_uri_characters`
// (codex-rs/skills/src/selection_tests.rs).
func TestFindSkillMentionsPreservesNativeURIPathCharactersLikeRust(t *testing.T) {
	skills := []appserver.SkillsListEntry{
		{Name: "odd-skill", Path: "/tmp/a b#c%/SKILL.md", Enabled: true},
	}
	literal := CollectToolMentions(`[$odd](skill:///tmp/a b#c%/SKILL.md)`, nil)
	if got := FindSkillMentions(literal, skills); len(got) != 1 || got[0].Name != "odd-skill" {
		t.Fatalf("literal path match = %#v, want odd-skill", got)
	}

	// A percent-encoded spelling names a different file, because `%` is literal.
	encoded := CollectToolMentions(`[$odd](skill:///tmp/a%20b#c%/SKILL.md)`, nil)
	if got := FindSkillMentions(encoded, skills); len(got) != 0 {
		t.Fatalf("percent-encoded path match = %#v, want none", got)
	}
}

// Rust #51482 shares one parsed identity per skill across the linked-path and
// name passes, so two catalog entries whose paths are equivalent Windows
// spellings are one skill (and the first native spelling wins). Counterpart Rust
// test: `same_name_skills_preserve_native_path_order`
// (codex-rs/skills/src/selection_tests.rs).
func TestFindSkillMentionsTreatsEquivalentPathsAsOneSkillLikeRust(t *testing.T) {
	skills := []appserver.SkillsListEntry{
		{Name: "foo", Path: `C:\Skills\Foo\SKILL.md`, Enabled: true},
		{Name: "foo", Path: `c:/skills/foo/SKILL.md`, Enabled: true},
	}
	mentions := CollectToolMentions("$foo [$foo](skill://C:\\Skills\\Foo\\SKILL.md)", nil)
	got := FindSkillMentions(mentions, skills)
	if len(got) != 1 {
		t.Fatalf("equivalent paths matched %d skills, want 1: %#v", len(got), got)
	}
	if got[0].Path != `C:\Skills\Foo\SKILL.md` {
		t.Fatalf("matched skill path = %q, want the first native spelling", got[0].Path)
	}
}

// Rust #51482 (chatwidget/input_submission.rs): the submitted skill list
// deduplicates by parsed path identity while the structured skill input keeps
// the native catalog spelling (`PathBuf::from(skill.path.as_str())`), because
// UserInput::Skill is still interpreted against the host filesystem. Counterpart
// Rust test: `async_question_answers_preserve_ambiguous_skill_selection_and_
// dismiss_...` (codex-rs/tui/src/chatwidget/tests/questions_tests.rs) together
// with the input_submission rewrite in this commit.
func TestSubmittedSkillInputsKeyByPathIdentityLikeRust(t *testing.T) {
	message := UserMessage{
		Text: "Use $foo and [$foo](skill://c:/skills/foo/SKILL.md)",
		// The composer binding carries the forward-slash spelling.
		MentionBindings: []string{"foo|c:/skills/foo/SKILL.md"},
	}
	decision := DecideUserMessageSubmission(message, UserMessageTextHistoryRecord(), SubmissionOptions{
		SessionConfigured:     true,
		CurrentModelHasImages: true,
		EffectiveModel:        "gpt-5",
		RequireModel:          true,
		MentionCatalog: SubmissionMentionCatalog{
			Skills: []appserver.SkillsListEntry{
				{Name: "foo", Path: `C:\Skills\Foo\SKILL.md`, Enabled: true},
			},
		},
	})
	if !decision.Accepted {
		t.Fatalf("submission rejected: %#v", decision)
	}
	skills := []SubmittedInputItem{}
	for _, item := range decision.Items {
		if item.Kind == SubmittedInputSkill {
			skills = append(skills, item)
		}
	}
	if len(skills) != 1 {
		t.Fatalf("submitted skills = %#v, want exactly one", skills)
	}
	if skills[0].Name != "foo" || skills[0].Path != `C:\Skills\Foo\SKILL.md` {
		t.Fatalf("submitted skill = %#v, want the native catalog spelling", skills[0])
	}
}
