package windowssandbox

import (
	"reflect"
	"testing"

	coresandbox "codex_go/sandbox"
)

// Explicit empty roots must survive the wrapper wire and capture boundary;
// only omitted roots retain the compatibility fallback to the working directory.
func TestWrapperPreservesExplicitEmptyWorkspaceRoots(t *testing.T) {
	for _, tc := range []struct {
		name        string
		roots, want []string
	}{
		{"omitted", nil, []string{`C:\repo`}},
		{"empty", []string{}, []string{}},
		{"selected", []string{`C:\selected`}, []string{`C:\selected`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := coresandbox.WorkspaceWritePermissionProfile()
			args, err := CreateWindowsSandboxCommandArgsForPermissionProfile(WindowsSandboxCommandArgsRequest{
				Command: []string{"cmd", "/c", "exit"}, CommandCWD: `C:\repo`,
				WorkspaceRoots: tc.roots, PermissionProfile: &profile,
				CodexHome: `C:\codex-home`, PrivateDesktopName: testPrivateDesktopName,
				WindowsSandboxLevel: WindowsSandboxLevelRestrictedToken,
			})
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := ParseWindowsSandboxWrapperArgs(args)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(parsed.WorkspaceRoots, tc.want) {
				t.Errorf("parsed roots = %#v, want %#v", parsed.WorkspaceRoots, tc.want)
			}
			capture := wrapperCaptureRequest(parsed)
			if !reflect.DeepEqual(capture.WorkspaceRoots, tc.want) {
				t.Errorf("capture roots = %#v, want %#v", capture.WorkspaceRoots, tc.want)
			}
		})
	}
}
