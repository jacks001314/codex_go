package appserver

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"codex_go/config"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

func TestExecPolicyAmendmentSavedIsInjectedAfterToolLikeRust(t *testing.T) {
	router := newExecPolicySavedTestRouter(t)
	router.rememberExecPolicyAmendmentSaved("thread-exec", "turn-exec", []string{"echo", "amendment-ok"})
	var persisted []session.Item
	postTool := router.execPolicyPostToolInputItems("thread-exec", "turn-exec", nil, func(items []session.Item) {
		persisted = append(persisted, items...)
	})
	input := postTool(context.Background(), nil, nil)
	want := "Approved command prefix saved:\n- [\"echo\", \"amendment-ok\"]"
	if len(input) != 1 || len(persisted) != 1 || persisted[0].Role != "developer" || persisted[0].Text != want {
		t.Fatalf("input=%#v persisted=%#v", input, persisted)
	}
	if got := inputMessageText(input[0]); got != want {
		t.Fatalf("input text = %q, want %q", got, want)
	}
	if repeated := postTool(context.Background(), nil, nil); len(repeated) != 0 {
		t.Fatalf("saved prefix was injected more than once: %#v", repeated)
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
	if fragments := router.execPolicySaved.take("thread-cyber", "turn-cyber"); len(fragments) != 0 {
		t.Fatalf("cyber-model approval saved reusable prefix fragments: %#v", fragments)
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
	fragments := router.execPolicySaved.take("thread-exec", "turn-exec")
	if len(fragments) != 1 || !reflect.DeepEqual(fragments[0].prefix, []string{"echo", "amendment-ok"}) {
		t.Fatalf("saved fragments = %#v", fragments)
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
