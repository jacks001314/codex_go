package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
)

// TestPermissionProfileArgsMatchRust mirrors the parser vectors of
// parse_permission_profile_args (codex-rs/tui/src/chatwidget/permission_discovery.rs:43-60),
// which tokenizes `/permissions` with the shared slash-command parser
// (chatwidget/slash_command_args.rs) of #54385.
func TestPermissionProfileArgsMatchRust(t *testing.T) {
	vectors := []struct {
		name        string
		args        string
		profile     string
		policy      string
		hasPolicy   bool
		reviewer    string
		hasReviewer bool
		failure     string
	}{
		{name: "profile only", args: ":read-only", profile: ":read-only"},
		{
			name: "space separated settings", args: ":workspace --approval-policy on-request --reviewer auto_review",
			profile: ":workspace", policy: "on-request", hasPolicy: true, reviewer: "auto_review", hasReviewer: true,
		},
		{
			name: "equals settings", args: ":workspace --approval-policy=never --reviewer=user",
			profile: ":workspace", policy: "never", hasPolicy: true, reviewer: "user", hasReviewer: true,
		},
		{
			name: "quoted profile", args: "'team review' --reviewer=user",
			profile: "team review", reviewer: "user", hasReviewer: true,
		},
		{
			name: "granular policy", args: `'team review' --approval-policy='{"granular":{"sandbox_approval":false,"rules":true,"mcp_elicitations":false}}'`,
			profile: "team review", policy: "granular", hasPolicy: true,
		},
		{name: "missing profile", args: "", failure: permissionProfileUsageMessage},
		{name: "extra positional", args: ":read-only extra", failure: "Unexpected permission argument `extra`."},
		{name: "literal separator", args: ":read-only -- --literal", failure: "Unexpected permission argument `--literal`."},
		{name: "unknown setting", args: ":read-only --bogus=1", failure: "Unknown command setting `--bogus`."},
		{name: "missing value", args: ":read-only --reviewer", failure: "Command setting `--reviewer` requires a value."},
		{
			name: "unknown reviewer", args: ":read-only --reviewer=bogus",
			failure: "Unknown permission reviewer `bogus`. Expected `user` or `auto_review`.",
		},
		{
			name: "unknown policy", args: ":read-only --approval-policy=bogus",
			failure: "Unknown approval policy `bogus`. Expected `untrusted`, `on-request`, `never`, or a granular policy.",
		},
		{
			name: "granular missing required flags", args: `:read-only --approval-policy='{"granular":{"rules":true}}'`,
			failure: "Unknown approval policy `{\"granular\":{\"rules\":true}}`. Expected `untrusted`, `on-request`, `never`, or a granular policy.",
		},
		{name: "unterminated quote", args: "'unfinished", failure: "Invalid command arguments. Check the quoting."},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			parsed, failure := parsePermissionProfileArgs(vector.args)
			if failure != vector.failure {
				t.Fatalf("failure = %q, want %q", failure, vector.failure)
			}
			if vector.failure != "" {
				return
			}
			if parsed.profileID != vector.profile {
				t.Fatalf("profile = %q, want %q", parsed.profileID, vector.profile)
			}
			if parsed.approvalPolicy != vector.policy || parsed.hasApprovalPolicy != vector.hasPolicy {
				t.Fatalf("policy = %q/%v, want %q/%v", parsed.approvalPolicy, parsed.hasApprovalPolicy, vector.policy, vector.hasPolicy)
			}
			if string(parsed.reviewer) != vector.reviewer || parsed.hasReviewer != vector.hasReviewer {
				t.Fatalf("reviewer = %q/%v, want %q/%v", parsed.reviewer, parsed.hasReviewer, vector.reviewer, vector.hasReviewer)
			}
		})
	}
}

// inlinePermissionsModel builds a model whose permissions picker knows the
// guardian-approval feature state.
func inlinePermissionsModel(t *testing.T, guardian bool, requirements *chatwidget.PermissionRequirements) *Model {
	t.Helper()
	// guardian_approval is default-on in the feature registry, so the test states
	// it explicitly in both directions.
	features := map[string]bool{"guardian_approval": guardian}
	state := codextui.NewState(nil)
	state.Sandbox = chatwidget.WorkspaceProfile
	state.ApprovalPolicy = string(chatwidget.ApprovalOnRequest)
	model := NewModel(state, Options{
		Width:                  120,
		Height:                 40,
		FeatureSettings:        features,
		PermissionRequirements: requirements,
	})
	model.State.SetThreadID("thread-a")
	return model
}

func runPermissionsCommand(t *testing.T, model *Model, args string) {
	t.Helper()
	runTeaCmd(t, model, model.applyPermissionsCommand(args))
}

// TestPermissionsCommandSelectsProfileInlineLikeRust covers #54383: the profile
// is selected without opening the picker, and the requested approval policy and
// reviewer override the built-in preset's own values.
func TestPermissionsCommandSelectsProfileInlineLikeRust(t *testing.T) {
	model := inlinePermissionsModel(t, true, nil)
	runPermissionsCommand(t, model, ":read-only")
	if model.State.Sandbox != chatwidget.ReadOnlyProfile {
		t.Fatalf("sandbox = %q, want %q", model.State.Sandbox, chatwidget.ReadOnlyProfile)
	}
	if model.State.ApprovalPolicy != string(chatwidget.ApprovalOnRequest) {
		t.Fatalf("approval = %q, want on-request", model.State.ApprovalPolicy)
	}
	if !strings.Contains(modelMessageText(model), "Permissions updated to Read Only") {
		t.Fatalf("history cell missing:\n%s", modelMessageText(model))
	}

	model = inlinePermissionsModel(t, true, nil)
	runPermissionsCommand(t, model, ":workspace --approval-policy=never --reviewer=user")
	if model.State.Sandbox != chatwidget.WorkspaceProfile {
		t.Fatalf("sandbox = %q, want %q", model.State.Sandbox, chatwidget.WorkspaceProfile)
	}
	if model.State.ApprovalPolicy != string(chatwidget.ApprovalNever) {
		t.Fatalf("requested approval policy was ignored: %q", model.State.ApprovalPolicy)
	}
	if model.approvalsReviewer != chatwidget.ApprovalsReviewerUser {
		t.Fatalf("reviewer = %q, want user", model.approvalsReviewer)
	}

	// A granular request resolves to the scalar the app server accepts for
	// granular profiles.
	model = inlinePermissionsModel(t, true, nil)
	runPermissionsCommand(t, model, `:workspace --approval-policy='{"granular":{"sandbox_approval":false,"rules":true,"mcp_elicitations":false}}'`)
	if model.State.ApprovalPolicy != "granular" {
		t.Fatalf("granular approval = %q, want granular", model.State.ApprovalPolicy)
	}
}

// TestPermissionsCommandApprovalAliasesLikeRust covers the `:ask-for-approval`
// and `:approve-for-me` shorthands (permissions_menu.rs:60-74).
func TestPermissionsCommandApprovalAliasesLikeRust(t *testing.T) {
	model := inlinePermissionsModel(t, true, nil)
	runPermissionsCommand(t, model, ":ask-for-approval")
	if model.State.Sandbox != chatwidget.WorkspaceProfile {
		t.Fatalf("alias sandbox = %q, want %q", model.State.Sandbox, chatwidget.WorkspaceProfile)
	}
	if model.approvalsReviewer != chatwidget.ApprovalsReviewerUser {
		t.Fatalf("alias reviewer = %q, want user", model.approvalsReviewer)
	}
	if !strings.Contains(modelMessageText(model), "Permissions updated to Ask for approval") {
		t.Fatalf("alias label missing:\n%s", modelMessageText(model))
	}

	model = inlinePermissionsModel(t, true, nil)
	runPermissionsCommand(t, model, ":approve-for-me")
	if model.State.Sandbox != chatwidget.WorkspaceProfile {
		t.Fatalf("approve-for-me sandbox = %q, want %q", model.State.Sandbox, chatwidget.WorkspaceProfile)
	}
	if model.approvalsReviewer != chatwidget.ApprovalsReviewerAutoReview {
		t.Fatalf("approve-for-me reviewer = %q, want auto_review", model.approvalsReviewer)
	}
	if !strings.Contains(modelMessageText(model), "Permissions updated to Approve for me") {
		t.Fatalf("approve-for-me label missing:\n%s", modelMessageText(model))
	}
}

// TestPermissionsCommandRejectsUnavailableAutoReviewLikeRust covers both
// guardian gates of #54383/#54385: the shorthand and an explicit
// `--reviewer=auto_review` are rejected while the feature is off.
func TestPermissionsCommandRejectsUnavailableAutoReviewLikeRust(t *testing.T) {
	const want = "Approve for me is unavailable. Run `/permissions` to see available profiles."
	for _, args := range []string{":approve-for-me", ":workspace --reviewer=auto_review"} {
		model := inlinePermissionsModel(t, false, nil)
		runPermissionsCommand(t, model, args)
		if model.notice != want {
			t.Fatalf("%q notice = %q, want %q", args, model.notice, want)
		}
		if model.State.Sandbox != chatwidget.WorkspaceProfile {
			t.Fatalf("%q changed the sandbox to %q", args, model.State.Sandbox)
		}
		if model.approvalsReviewer != chatwidget.ApprovalsReviewerUser {
			t.Fatalf("%q changed the reviewer to %q", args, model.approvalsReviewer)
		}
	}
}

// TestPermissionsCommandRejectsUnknownAndDisabledProfilesLikeRust covers the
// two rejection snapshots of #54383/#54385
// (permissions_command_unknown_profile / permissions_command_disabled_profile).
func TestPermissionsCommandRejectsUnknownAndDisabledProfilesLikeRust(t *testing.T) {
	model := inlinePermissionsModel(t, true, nil)
	runPermissionsCommand(t, model, "unknown-profile")
	if model.notice != "Unknown permission profile `unknown-profile`. Run `/permissions` to see available profiles." {
		t.Fatalf("unknown profile notice = %q", model.notice)
	}

	model = inlinePermissionsModel(t, true, &chatwidget.PermissionRequirements{
		AllowedProfiles: map[string]bool{chatwidget.ReadOnlyProfile: false},
	})
	runPermissionsCommand(t, model, ":read-only")
	if model.notice != "Permission profile `:read-only` is unavailable: Disabled by requirements." {
		t.Fatalf("disabled profile notice = %q", model.notice)
	}
	if model.State.Sandbox != chatwidget.WorkspaceProfile {
		t.Fatalf("disabled profile changed the sandbox to %q", model.State.Sandbox)
	}

	// The preset's own approval policy is what the requirements are checked
	// against when the request does not name one.
	model = inlinePermissionsModel(t, true, &chatwidget.PermissionRequirements{
		AllowedApprovalPolicies: []chatwidget.ApprovalPolicy{chatwidget.ApprovalOnRequest},
	})
	runPermissionsCommand(t, model, chatwidget.DangerFullAccessProfile)
	if model.notice != "Permission profile `:danger-full-access` is unavailable: Disabled by requirements." {
		t.Fatalf("policy-disabled profile notice = %q", model.notice)
	}
}

// TestPermissionsCommandKeepsFullAccessConfirmationLikeRust covers #54385's
// "keep full-access confirmation for every reviewer".
func TestPermissionsCommandKeepsFullAccessConfirmationLikeRust(t *testing.T) {
	model := inlinePermissionsModel(t, true, nil)
	runPermissionsCommand(t, model, chatwidget.DangerFullAccessProfile+" --reviewer=auto_review")
	if model.modal == nil || model.modal.kind != ModalKindPermissions {
		t.Fatalf("full access must open its confirmation, modal = %#v", model.modal)
	}
	if !strings.Contains(model.View(), "Enable full access?") {
		t.Fatalf("confirmation view missing:\n%s", model.View())
	}
	if model.State.Sandbox != chatwidget.WorkspaceProfile {
		t.Fatalf("full access applied before confirmation: %q", model.State.Sandbox)
	}
	model.Update(key(bubbletea.KeyEnter))
	if model.State.Sandbox != chatwidget.DangerFullAccessProfile {
		t.Fatalf("confirmed sandbox = %q, want %q", model.State.Sandbox, chatwidget.DangerFullAccessProfile)
	}
	if model.State.ApprovalPolicy != string(chatwidget.ApprovalNever) {
		t.Fatalf("confirmed approval = %q, want never", model.State.ApprovalPolicy)
	}
	if model.approvalsReviewer != chatwidget.ApprovalsReviewerAutoReview {
		t.Fatalf("confirmed reviewer = %q, want auto_review", model.approvalsReviewer)
	}
}

// TestPermissionsCommandDiscoversCatalogForInlineRequestLikeRust covers the
// pending state of #54383 (InlinePermissionProfileSelection::Discovering): a
// custom profile the client has not discovered yet is selected after the
// catalog arrives, through the server.
func TestPermissionsCommandDiscoversCatalogForInlineRequestLikeRust(t *testing.T) {
	recorder := &permissionProfileRecorder{profiles: []chatwidget.CustomPermissionProfile{
		{ID: "team review", Description: "Team review profile", Allowed: true},
	}}
	model := recorder.model(false)
	runPermissionsCommand(t, model, `'team review' --approval-policy=on-request --reviewer=user`)
	if recorder.lists != 1 {
		t.Fatalf("discovery calls = %d, want 1", recorder.lists)
	}
	if len(recorder.updates) != 1 || recorder.updates[0] != "thread-a|team review" {
		t.Fatalf("server selections = %#v", recorder.updates)
	}
	if model.pendingServerProfile != "team review" {
		t.Fatalf("pending profile = %q", model.pendingServerProfile)
	}
	if model.State.Sandbox == "team review" {
		t.Fatal("a server profile must not be applied locally before confirmation")
	}
}

// TestPermissionsCommandWithoutArgumentsOpensPickerLikeRust keeps the bare
// command behaviour.
func TestPermissionsCommandWithoutArgumentsOpensPickerLikeRust(t *testing.T) {
	model := inlinePermissionsModel(t, false, nil)
	runPermissionsCommand(t, model, "")
	if model.modal == nil || model.modal.kind != ModalKindPermissions {
		t.Fatalf("bare /permissions must open the picker, modal = %#v", model.modal)
	}
	if !strings.Contains(model.View(), "Update Model Permissions") {
		t.Fatalf("picker missing:\n%s", model.View())
	}
}

// TestPermissionsCommandDispatchesInlineArgumentsLikeRust pins the contract that
// makes `/permissions <profile-id>` dispatch locally instead of being submitted as
// a prompt (Rust slash_command.rs:177 supports_inline_args).
func TestPermissionsCommandDispatchesInlineArgumentsLikeRust(t *testing.T) {
	if !chatwidget.CommandSupportsInlineArgs(codextui.CommandPermissions) {
		t.Fatal("chatwidget must dispatch `/permissions` arguments locally")
	}
	permissionsItem := bottompane.SlashCommandItem{Command: codextui.CommandPermissions}
	if !permissionsItem.SupportsInlineArgs() {
		t.Fatal("the command catalog must accept `/permissions` inline arguments")
	}
}

// TestPermissionsCommandRoutingLikeRust drives the command through the composer,
// so the model dispatch of `/permissions` arguments is covered too (Rust
// SlashCommand::Permissions dispatch_command_with_args).
func TestPermissionsCommandRoutingLikeRust(t *testing.T) {
	model := inlinePermissionsModel(t, false, nil)
	typeText(t, model, "/permissions :read-only")
	model.Update(key(bubbletea.KeyEnter))
	if model.State.Sandbox != chatwidget.ReadOnlyProfile {
		t.Fatalf("typed inline request sandbox = %q, want %q", model.State.Sandbox, chatwidget.ReadOnlyProfile)
	}

	// A bare `/permissions` still opens the picker.
	typeText(t, model, "/permissions")
	model.Update(key(bubbletea.KeyEnter))
	if model.modal == nil || model.modal.kind != ModalKindPermissions {
		t.Fatalf("typed bare command modal = %#v, want the permissions picker", model.modal)
	}
}
