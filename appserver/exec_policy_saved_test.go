package appserver

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

// TestExecPolicyAmendmentSavedReportsThroughWorldStateLikeRust covers the
// Rust ApprovedCommandPrefixSaved behavior: a newly approved prefix is reported
// once by the permissions world-state diff (the instructions hash excludes the
// prefixes, so only the prefix delta changes).
func TestExecPolicyAmendmentSavedReportsThroughWorldStateLikeRust(t *testing.T) {
	router := newExecPolicySavedTestRouter(t)
	const threadID = "thread-exec"
	now := time.Now().UTC()
	if err := router.services.ThreadRouter.store.Create(&session.Record{
		ID: session.ThreadID(threadID), SessionID: threadID, CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{HistoryMode: string(ThreadHistoryLegacy), Extra: map[string]any{}},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	params := &turn.TurnStartParams{ThreadID: threadID, CWD: router.services.DefaultCWD}
	cfg := &config.Config{Values: map[string]any{}}

	// The first turn emits the full instructions (which list the approved
	// prefix) and persists the section snapshot.
	first, err := router.permissionsWorldStateInputItem(threadID, params, nil, cfg)
	if err != nil {
		t.Fatalf("permissionsWorldStateInputItem() error = %v", err)
	}
	if first == nil || !strings.Contains(permissionsInputItemText(t, first), sandbox.PermissionInstructionsOpenTag) {
		t.Fatalf("first section = %#v", first)
	}
	router.rememberExecPolicyAmendmentSaved(threadID, "turn-exec", []string{"echo", "amendment-ok"})

	// The instructions hash is unchanged, so only the newly approved prefix is
	// reported, exactly once.
	second, err := router.permissionsWorldStateInputItem(threadID, params, nil, cfg)
	if err != nil {
		t.Fatalf("permissionsWorldStateInputItem() error = %v", err)
	}
	want := "Approved command prefix saved:\n- [\"echo\", \"amendment-ok\"]"
	if got := permissionsInputItemText(t, second); got != want {
		t.Fatalf("saved prefix text = %q, want %q", got, want)
	}
	third, err := router.permissionsWorldStateInputItem(threadID, params, nil, cfg)
	if err != nil {
		t.Fatalf("permissionsWorldStateInputItem() error = %v", err)
	}
	if third != nil {
		t.Fatalf("saved prefix was reported more than once: %#v", third)
	}
}

func inputMessageText(value any) string {
	payload, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	var part map[string]any
	if content, ok := payload["content"].([]map[string]any); ok && len(content) == 1 {
		part = content[0]
	} else if content, ok := payload["content"].([]any); ok && len(content) == 1 {
		part, _ = content[0].(map[string]any)
	}
	if part == nil {
		return ""
	}
	text, _ := part["text"].(string)
	return text
}

func TestShellApprovalSkipsExecPolicyAmendmentForCyberModelLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("approval_policy = \"on_request\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), DefaultCWD: home})
	var received *CommandExecutionRequestApprovalParams
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		if request.Method == ServerRequestCommandExecutionApproval && request.Params != nil {
			received, _ = request.Params.(*CommandExecutionRequestApprovalParams)
		}
		router.requireServerRequests().Resolve(OK(request.ID, &CommandExecutionRequestApprovalResponse{
			Decision: CommandExecutionApprovalAccept,
		}))
	}))
	approval := router.shellApprovalForTurn("thread-cyber", "turn-cyber", true)
	decision, err := approval(context.Background(), &tool.ShellApprovalRequest{
		Request: &tool.ShellRequest{
			HookCommand:    "echo amendment-ok",
			CWD:            home,
			ApprovalReason: "requires approval",
			PrefixRule:     []string{"echo", "amendment-ok"},
		},
		Invocation: &tool.Invocation{CallID: "call-cyber"},
	})
	if err != nil || !decision.Approved {
		t.Fatalf("approval decision = %#v err = %v", decision, err)
	}
	if received == nil || len(stringSliceFromAny(received.ProposedExecPolicyAmendment)) != 0 {
		t.Fatalf("cyber-model approval proposed an exec-policy amendment: %#v", received)
	}
	if prefixes := router.execPolicySaved.approvedPrefixes("thread-cyber"); len(prefixes) != 0 {
		t.Fatalf("cyber-model approval saved reusable prefixes: %#v", prefixes)
	}
}

func TestShellApprovalWithExecpolicyAmendmentRemembersPrefix(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("approval_policy = \"on_request\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), DefaultCWD: home})
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		decision := map[string]any{
			string(CommandExecutionApprovalAcceptWithExecpolicyAmendment): map[string]any{
				"execpolicy_amendment": []string{"echo", "amendment-ok"},
			},
		}
		response := OK(request.ID, &CommandExecutionRequestApprovalResponse{Decision: decision})
		router.requireServerRequests().Resolve(response)
	}))
	approval := router.shellApprovalForTurn("thread-exec", "turn-exec", false)
	decision, err := approval(context.Background(), &tool.ShellApprovalRequest{
		Request: &tool.ShellRequest{
			HookCommand:    "echo amendment-ok",
			CWD:            home,
			ApprovalReason: "requires approval",
		},
		Invocation: &tool.Invocation{CallID: "call-exec-amend"},
	})
	if err != nil || !decision.Approved {
		t.Fatalf("approval decision = %#v err = %v", decision, err)
	}
	prefixes := router.execPolicySaved.approvedPrefixes("thread-exec")
	if len(prefixes) != 1 || !reflect.DeepEqual(prefixes[0], []string{"echo", "amendment-ok"}) {
		t.Fatalf("saved prefixes = %#v", prefixes)
	}
}

func TestCommandExecutionApprovalDecisionExecpolicyAmendmentParsesShapes(t *testing.T) {
	for name, decision := range map[string]any{
		"string slice": map[string]any{
			string(CommandExecutionApprovalAcceptWithExecpolicyAmendment): map[string]any{
				"execpolicy_amendment": []string{"echo", "amendment-ok"},
			},
		},
		"any slice": map[string]any{
			string(CommandExecutionApprovalAcceptWithExecpolicyAmendment): map[string]any{
				"execpolicy_amendment": []any{"echo", "amendment-ok"},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := commandExecutionApprovalDecisionExecpolicyAmendment(decision)
			if !reflect.DeepEqual(got, []string{"echo", "amendment-ok"}) {
				t.Fatalf("amendment = %#v", got)
			}
		})
	}
	if got := commandExecutionApprovalDecisionExecpolicyAmendment(map[string]any{string(CommandExecutionApprovalAccept): true}); got != nil {
		t.Fatalf("plain accept produced amendment %#v", got)
	}
}

func newExecPolicySavedTestRouter(t *testing.T) *RuntimeRouter {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("approval_policy = \"on_request\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		DefaultCWD:   home,
	})
	return router
}
