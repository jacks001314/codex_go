package execserver

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFileSystemSandboxContextWindowsProxySettingsModeWireShape(t *testing.T) {
	context := FileSystemSandboxContext{
		Permissions:                     json.RawMessage(`{"file_system":{"mode":"readOnly"}}`),
		WindowsSandboxLevel:             "elevated",
		WindowsSandboxProxySettingsMode: WindowsSandboxProxySettingsPreserve,
	}
	data, err := json.Marshal(context)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"windowsSandboxProxySettingsMode":"preserve"`) {
		t.Fatalf("sandbox context JSON = %s", data)
	}
	var decoded FileSystemSandboxContext
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.WindowsSandboxProxySettingsMode != WindowsSandboxProxySettingsPreserve {
		t.Fatalf("decoded mode = %q", decoded.WindowsSandboxProxySettingsMode)
	}
}

func TestWindowsSandboxProxySettingsModeDefaultsToReconcileAndRejectsUnknown(t *testing.T) {
	if err := WindowsSandboxProxySettingsMode("").Validate(); err != nil {
		t.Fatalf("default mode error = %v", err)
	}
	if err := WindowsSandboxProxySettingsReconcile.Validate(); err != nil {
		t.Fatalf("reconcile mode error = %v", err)
	}
	if err := WindowsSandboxProxySettingsMode("unknown").Validate(); err == nil {
		t.Fatal("unknown mode unexpectedly accepted")
	}
}
