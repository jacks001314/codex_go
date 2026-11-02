package tool

import (
	"testing"

	"codex_go/sandbox"
)

func TestShellApprovalKeyIdentifiesOneLaunch(t *testing.T) {
	base := &ShellRequest{
		Command:            []string{"git", "status"},
		HookCommand:        "git status",
		CWD:                `D:\repo`,
		SandboxPermissions: sandbox.SandboxPermissionsRequireEscalated,
	}
	if key := ShellApprovalKey(base); key == "" {
		t.Fatal("ShellApprovalKey() = empty")
	} else if again := ShellApprovalKey(base); again != key {
		t.Fatalf("ShellApprovalKey() is not stable: %q != %q", key, again)
	}

	for name, mutate := range map[string]func(*ShellRequest){
		"command": func(request *ShellRequest) {
			request.Command = []string{"git", "push"}
			request.HookCommand = "git push"
		},
		"cwd":                 func(request *ShellRequest) { request.CWD = `D:\other` },
		"sandbox permissions": func(request *ShellRequest) { request.SandboxPermissions = sandbox.SandboxPermissionsUseDefault },
		"tty":                 func(request *ShellRequest) { request.TTY = true },
		"environment":         func(request *ShellRequest) { request.UnifiedExecEnvironmentID = "remote" },
	} {
		t.Run(name, func(t *testing.T) {
			request := *base
			mutate(&request)
			if ShellApprovalKey(&request) == ShellApprovalKey(base) {
				t.Fatalf("ShellApprovalKey() ignored the %s", name)
			}
		})
	}
}

func TestShellApprovalMatchesPrefixOnlyOneParsedCommand(t *testing.T) {
	prefix := []string{"git", "fetch", "origin", "main"}
	for name, test := range map[string]struct {
		command string
		want    bool
	}{
		"exact":           {command: "git fetch origin main", want: true},
		"extra arguments": {command: "git fetch origin main --tags", want: true},
		"different":       {command: "git push origin main", want: false},
		"shorter":         {command: "git fetch", want: false},
		"chained":         {command: "git fetch origin main && rm -rf build", want: false},
		"semicolon chain": {command: "git fetch origin main; rm -rf build", want: false},
		"pipeline":        {command: "git fetch origin main | tee log", want: false},
		"no space chain":  {command: "git fetch origin main&&rm -rf build", want: false},
	} {
		t.Run(name, func(t *testing.T) {
			request := &ShellRequest{HookCommand: test.command}
			if got := ShellApprovalMatchesPrefix(request, prefix); got != test.want {
				t.Fatalf("ShellApprovalMatchesPrefix(%q) = %v, want %v", test.command, got, test.want)
			}
		})
	}
	if ShellApprovalMatchesPrefix(nil, prefix) || ShellApprovalMatchesPrefix(&ShellRequest{HookCommand: "git fetch origin main"}, nil) {
		t.Fatal("ShellApprovalMatchesPrefix() accepted a missing request or prefix")
	}
}
