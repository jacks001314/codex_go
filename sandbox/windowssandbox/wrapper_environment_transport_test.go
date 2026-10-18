package windowssandbox

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	coresandbox "codex_go/sandbox"
)

func TestWindowsSandboxWrapperUsesEnvironmentForLargePayloadLikeRust(t *testing.T) {
	profile := coresandbox.ReadOnlyPermissionProfile()
	req := WindowsSandboxCommandArgsRequest{
		Command:             []string{"cmd.exe", "/c", strings.Repeat("x", 40_000)},
		CommandCWD:          `C:\workspace`,
		Env:                 map[string]string{"WORKLOAD": "preserved"},
		PermissionProfile:   &profile,
		WindowsSandboxLevel: WindowsSandboxLevelRestrictedToken,
		CodexHome:           `C:\codex`,
	}
	args, err := CreateWindowsSandboxCommandArgsForPermissionProfile(req)
	if err != nil {
		t.Fatalf("CreateWindowsSandboxCommandArgsForPermissionProfile() error = %v", err)
	}
	if !reflect.DeepEqual(args, []string{CodexWindowsSandboxArg1, launchPayloadEnvFlag}) {
		t.Fatalf("wrapper args = %#v", args)
	}
	env := SandboxLaunchEnvironmentFromEnviron(SandboxLaunchEnvironFromMap(req.Env))
	payload, err := DecodeAndScrubSandboxLaunchPayload(env)
	if err != nil {
		t.Fatalf("DecodeAndScrubSandboxLaunchPayload() error = %v", err)
	}
	var decoded []string
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseWindowsSandboxWrapperArgs(append([]string{CodexWindowsSandboxArg1}, decoded...))
	if err != nil {
		t.Fatalf("ParseWindowsSandboxWrapperArgs() error = %v", err)
	}
	if !reflect.DeepEqual(parsed.Command, req.Command) || parsed.Env["WORKLOAD"] != "preserved" {
		t.Fatalf("parsed request = %#v", parsed)
	}
}
