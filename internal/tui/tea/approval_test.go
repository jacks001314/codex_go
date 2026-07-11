package tea

import "testing"

func TestApprovalCommandTextUsesRustShellDisplayHelper(t *testing.T) {
	if got := approvalCommandText(map[string]any{"hook_command": "custom hook"}); got != "custom hook" {
		t.Fatalf("hook command = %q", got)
	}
	if got := approvalCommandText(map[string]any{"command": []string{"bash", "-lc", "go test ./..."}}); got != "go test ./..." {
		t.Fatalf("bash command = %q", got)
	}
	if got := approvalCommandText(map[string]any{"command": []string{"pwsh", "-NoProfile", "-Command", "Get-ChildItem"}}); got != "Get-ChildItem" {
		t.Fatalf("powershell command = %q", got)
	}
	if got := approvalCommandText(map[string]any{"command": []string{"fish", "-lc", "echo hello"}}); got != "fish -lc 'echo hello'" {
		t.Fatalf("fish command = %q", got)
	}
	if got := approvalCommandText(map[string]any{"command": []string{"foo", "bar baz", "weird&stuff"}}); got != "foo 'bar baz' 'weird&stuff'" {
		t.Fatalf("fallback command = %q", got)
	}
}
