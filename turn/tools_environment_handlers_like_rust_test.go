package turn

import (
	"context"
	"strings"
	"testing"

	"codex_go/tool"
)

const applyPatchWithEnvironmentID = `*** Begin Patch
*** Environment ID: remote
*** Delete File: old.txt
*** End Patch`

// Rust #50962: the readiness facts the tool spec plan computes have to reach the
// environment-backed handlers that do not run through the shell executor, so an
// explicit environment id is resolved instead of being silently ignored. A turn
// whose only selected environment has no usable executor reports the shared
// waiting message once `stable_environment_tools` is on.
func TestEnvironmentBackedHandlersResolveTheirEnvironmentLikeRust(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableUnifiedExec = true
	options.SelectedEnvironmentIDs = []string{"remote"}
	options.EnvironmentWaiter = waitingEnvironmentWaiter{}
	options.StableEnvironmentTools = true
	options.EnableRequestPermissions = true
	options.RequestPermissionsReviewer = readyRequestPermissionsReviewer
	options.ViewImage = &tool.ViewImageOptions{CWD: options.Shell.Validation.CWD}

	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}

	cases := []struct {
		toolName  string
		arguments string
	}{
		{tool.ViewImageToolName, `{"path":"note.png","environment_id":"remote"}`},
		{tool.RequestPermissionsToolName, `{"environment_id":"remote","permissions":{"network":{"enabled":true}}}`},
		{tool.DefaultApplyPatchToolName, applyPatchWithEnvironmentID},
	}
	for _, testCase := range cases {
		t.Run(testCase.toolName, func(t *testing.T) {
			executor, ok := registry.Lookup(tool.PlainName(testCase.toolName))
			if !ok {
				t.Fatalf("%s was not registered", testCase.toolName)
			}
			_, err := executor.Execute(context.Background(), &tool.Invocation{
				ToolName: tool.PlainName(testCase.toolName),
				Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: testCase.arguments},
			})
			if err == nil || !strings.Contains(err.Error(), tool.UnifiedUnavailableEnvironmentMessage) {
				t.Fatalf("%s error = %v, want the shared waiting message", testCase.toolName, err)
			}
		})
	}
}
