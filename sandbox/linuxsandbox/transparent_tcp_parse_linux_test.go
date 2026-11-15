package linuxsandbox

import (
	"io"
	"reflect"
	"testing"
)

func TestParseLinuxSandboxTransparentTCPLikeRust(t *testing.T) {
	command, err := parseLinuxSandboxCommand([]string{
		"--sandbox-policy-cwd", "/tmp",
		"--permission-profile", `{"type":"managed","file_system":{"type":"unrestricted"},"network":"enabled"}`,
		"--allow-network-for-proxy",
		"--transparent-tcp",
		"--transparent-tcp-fd", "7,8,9",
		"--",
		"true",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseLinuxSandboxCommand() error = %v", err)
	}
	if !command.TransparentTCP {
		t.Fatal("TransparentTCP = false")
	}
	if !reflect.DeepEqual(command.TransparentTCPFDs, []int{7, 8, 9}) {
		t.Fatalf("TransparentTCPFDs = %#v", command.TransparentTCPFDs)
	}
	if _, err := parseLinuxSandboxCommand([]string{
		"--sandbox-policy-cwd", "/tmp",
		"--permission-profile", `{"type":"managed","file_system":{"type":"unrestricted"},"network":"enabled"}`,
		"--transparent-tcp-fd", "not-a-number",
		"--", "true",
	}, io.Discard); err == nil {
		t.Fatal("parseLinuxSandboxCommand() accepted a malformed descriptor list")
	}
}
