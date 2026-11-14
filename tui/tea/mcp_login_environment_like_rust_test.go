package tea

import "testing"

// Rust #61257: /mcp login accepts an explicit registration scope.
func TestMCPLoginCommandAcceptsEnvironmentLikeRust(t *testing.T) {
	var gotName, gotEnvironment string
	model := mcpLoginTestModel(t, func(name, environmentID, threadID string) (MCPLoginStart, error) {
		gotName, gotEnvironment = name, environmentID
		return MCPLoginStart{LoginID: "login-env"}, nil
	})
	command := model.applyMCPCommand("login docs --environment env-a")
	if command == nil || model.pendingMCPLoginStart == nil {
		t.Fatalf("start cmd=%v pending=%#v", command, model.pendingMCPLoginStart)
	}
	message, ok := command().(MCPLoginStartedMsg)
	if !ok {
		t.Fatalf("start message = %T", command())
	}
	if message.Name != "docs" || message.EnvironmentID != "env-a" || message.ThreadID != "thread-login" {
		t.Fatalf("start message = %+v", message)
	}
	if gotName != "docs" || gotEnvironment != "env-a" {
		t.Fatalf("starter args = %q %q", gotName, gotEnvironment)
	}
	if pending := model.pendingMCPLoginStart; pending == nil || pending.environmentID != "env-a" {
		t.Fatalf("pending scope = %#v", pending)
	}
	model.applyMCPLoginStarted(message)

	equalsForm := model.applyMCPCommand("login docs --environment=env-b")
	if equalsForm == nil {
		t.Fatal("--environment=<id> form was rejected")
	}
	if pending := model.pendingMCPLoginStart; pending == nil || pending.environmentID != "env-b" {
		t.Fatalf("equals-form scope = %#v", pending)
	}
	model.pendingMCPLoginStart = nil

	if invalid := model.applyMCPCommand("login docs --environment"); invalid != nil {
		t.Fatal("missing environment value was accepted")
	}
	if invalid := model.applyMCPCommand("login docs --bogus env-a"); invalid != nil {
		t.Fatal("unknown login flag was accepted")
	}
}
