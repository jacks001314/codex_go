package tool

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestViewImageRoutesToSelectedRemoteEnvironmentLikeRust covers Rust #20647
// (`78421face0`, "Route process tools to selected environments") for view_image:
// the handler reads through the selected environment's filesystem
// (`turn_environment.environment.get_filesystem()`,
// codex-rs/core/src/tools/handlers/view_image.rs:158).
//
// Compared with the Rust integration tests
// `view_image_routes_to_selected_remote_environment`
// (codex-rs/core/tests/suite/view_image.rs:731) and
// `view_image_routes_to_selected_local_environment` (:586):
//   - an explicit `environment_id` reads that environment's filesystem;
//   - an omitted `environment_id` resolves to Rust's `primary()` = the first
//     *ready* environment in selection order, so a turn whose only ready
//     environment is remote reads remotely instead of falling back to the local
//     process filesystem;
//   - the local environment still reads the process filesystem.
//
// The test drives a real in-process exec server, and each environment has a
// distinct directory, so a mis-route is observable.
func TestViewImageRoutesToSelectedRemoteEnvironmentLikeRust(t *testing.T) {
	serverURL, stop := startEnvironmentFileSystemExecServer(t)
	defer stop()

	remoteDir := t.TempDir()
	localDir := t.TempDir()
	imageBytes, err := base64.StdEncoding.DecodeString(tinyPNG)
	if err != nil {
		t.Fatalf("decode tiny PNG: %v", err)
	}
	if err := os.WriteFile(filepath.Join(remoteDir, "remote.png"), imageBytes, 0o600); err != nil {
		t.Fatalf("write remote image: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "local.png"), imageBytes, 0o600); err != nil {
		t.Fatalf("write local image: %v", err)
	}

	handler := NewViewImageHandler(ViewImageOptions{
		CWD:                  localDir,
		IncludeEnvironmentID: true,
		EnvironmentCheck: &UnifiedExecEnvironmentCheck{
			SelectedEnvironmentIDs: []string{"remote-x"},
			ReadyEnvironmentCount:  1,
			StableEnvironmentTools: true,
		},
		EnvironmentFileSystems: NewUnifiedExecEnvironmentFileSystems([]UnifiedExecEnvironment{{
			ID:            "remote-x",
			CWD:           remoteDir,
			ExecServerURL: serverURL,
		}}, localDir),
	})

	// Explicit selector: read the executor's copy of the image.
	assertViewImageReadsImageLikeRust(t, handler, `{"path":"remote.png","environment_id":"remote-x"}`, imageBytes)

	// Implicit primary: Rust's `primary()` picks the first ready environment, so
	// the read resolves to the remote executor. `remote.png` does not exist in
	// the local directory, so the pre-#20647 local fallback would fail here.
	assertViewImageReadsImageLikeRust(t, handler, `{"path":"remote.png"}`, imageBytes)

	// Explicit local selector: read the process filesystem.
	assertViewImageReadsImageLikeRust(t, handler, `{"path":"local.png","environment_id":"local"}`, imageBytes)

	// The remote environment cannot see the local-only file: the call routed to
	// the executor rather than the process filesystem.
	if _, err := handler.Execute(context.Background(), &Invocation{Payload: Payload{Kind: PayloadFunction, Arguments: `{"path":"local.png","environment_id":"remote-x"}`}}); err == nil {
		t.Fatalf("view_image read the local-only file through the remote environment")
	}
}

func assertViewImageReadsImageLikeRust(t *testing.T, handler *ViewImageHandler, arguments string, want []byte) {
	t.Helper()
	out, err := handler.Execute(context.Background(), &Invocation{Payload: Payload{Kind: PayloadFunction, Arguments: arguments}})
	if err != nil {
		t.Fatalf("Execute(%s) error = %v", arguments, err)
	}
	const prefix = "data:application/octet-stream;base64,"
	url, _ := out.Data["image_url"].(string)
	if !strings.HasPrefix(url, prefix) {
		t.Fatalf("Execute(%s) image_url = %q, want the octet-stream data URL", arguments, url)
	}
	got, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, prefix))
	if err != nil {
		t.Fatalf("Execute(%s) decode image_url: %v", arguments, err)
	}
	if string(got) != string(want) {
		t.Fatalf("Execute(%s) image bytes = %q, want %q", arguments, got, want)
	}
}
