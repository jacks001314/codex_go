package parity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"codex_go/execserver"
)

func TestRustExecServerProtocolSurfaceAgainstGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	source := string(mustReadParityFile(t, filepath.Join(root, "exec-server-protocol", "src", "protocol.rs")))
	methodPattern := regexp.MustCompile(`pub const [A-Z0-9_]+_METHOD: &str = "([^"]+)";`)
	matches := methodPattern.FindAllStringSubmatch(source, -1)
	rustMethods := make([]string, 0, len(matches))
	for _, match := range matches {
		rustMethods = append(rustMethods, match[1])
	}
	goMethods := []string{
		execserver.MethodInitialize,
		execserver.MethodInitialized,
		execserver.MethodProcessStart,
		execserver.MethodProcessRead,
		execserver.MethodProcessWrite,
		execserver.MethodProcessSignal,
		execserver.MethodProcessTerminate,
		execserver.MethodProcessOutput,
		execserver.MethodProcessExited,
		execserver.MethodProcessClosed,
		execserver.MethodEnvironmentInfo,
		execserver.MethodEnvironmentStatus,
		execserver.MethodFSReadFile,
		execserver.MethodFSOpen,
		execserver.MethodFSReadBlock,
		execserver.MethodFSClose,
		execserver.MethodFSWriteFile,
		execserver.MethodFSCreateDirectory,
		execserver.MethodFSGetMetadata,
		execserver.MethodFSCanonicalize,
		execserver.MethodFSReadDirectory,
		execserver.MethodFSWalk,
		execserver.MethodFSRemove,
		execserver.MethodFSCopy,
		execserver.MethodCapabilityRootsDiscover,
		execserver.MethodHTTPRequest,
		execserver.MethodHTTPRequestBodyDelta,
	}
	sort.Strings(rustMethods)
	sort.Strings(goMethods)
	if !reflect.DeepEqual(goMethods, rustMethods) {
		t.Fatalf("exec-server method surface drift\nGo:   %v\nRust: %v", goMethods, rustMethods)
	}

	for _, manifestPath := range []string{
		".codex-plugin/plugin.json",
		".claude-plugin/plugin.json",
		".cursor-plugin/plugin.json",
	} {
		if !strings.Contains(source, `"`+manifestPath+`"`) {
			t.Fatalf("Rust discoverable plugin manifest %q is missing", manifestPath)
		}
	}

	payload, err := json.Marshal(execserver.CapabilityRootsDiscoverResponse{Roots: []execserver.CapabilityRootDiscovery{}})
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"roots":[]}` {
		t.Fatalf("empty capabilityRoots/discoverV1 response = %s", payload)
	}
}

func mustReadParityFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return data
}
