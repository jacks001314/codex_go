package execserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Rust metadata_batch_fits: one batch is bounded by both the path count and the
// encoded request bytes.
func TestMetadataBatchFitsLikeRust(t *testing.T) {
	if !metadataBatchFits(nil) || !metadataBatchFits([]string{"file:///a", "file:///b"}) {
		t.Fatal("a small batch must fit")
	}
	tooMany := make([]string, MaxMetadataBatchProbes+1)
	for index := range tooMany {
		tooMany[index] = "f"
	}
	if metadataBatchFits(tooMany) {
		t.Fatalf("a batch of %d paths must not fit", len(tooMany))
	}
	if metadataBatchFits([]string{strings.Repeat("x", MaxMetadataBatchBytes+1)}) {
		t.Fatal("a batch over the byte budget must not fit")
	}
}

// Rust FsGetMetadataBatchResponse serializes Result<FileMetadata,
// JSONRPCErrorError> in serde's externally tagged shape.
func TestMetadataBatchResultWireShapeLikeRust(t *testing.T) {
	metadata := FSGetMetadataResponse{IsFile: true, Size: 7}
	encoded, err := json.Marshal(FSGetMetadataBatchResponse{Results: []FSMetadataBatchResult{
		{Metadata: &metadata},
		{Error: &FSBatchError{Code: -32004, Message: "missing"}},
	}})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"Ok":{`) || !strings.Contains(string(encoded), `"isFile":true`) ||
		!strings.Contains(string(encoded), `"Err":{"code":-32004`) {
		t.Fatalf("encoded batch = %s", encoded)
	}
	var decoded FSGetMetadataBatchResponse
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(decoded.Results) != 2 || decoded.Results[0].Metadata == nil || decoded.Results[0].Metadata.Size != 7 {
		t.Fatalf("decoded ok arm = %+v", decoded.Results)
	}
	if decoded.Results[1].Error == nil || decoded.Results[1].Error.Code != -32004 {
		t.Fatalf("decoded err arm = %+v", decoded.Results[1])
	}
}

// Rust FileSystemHandler::get_metadata_batch: results keep request order and
// each path reports its own error instead of failing the batch.
func TestFSGetMetadataBatchPreservesOrderAndErrorsLikeRust(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	missingPath := filepath.Join(dir, "missing.txt")
	response, err := getMetadataBatch(&FSGetMetadataBatchParams{Paths: []string{
		fileURIForMetadataBatchTest(filePath),
		fileURIForMetadataBatchTest(missingPath),
		fileURIForMetadataBatchTest(dir),
	}})
	if err != nil {
		t.Fatalf("getMetadataBatch() error = %v", err)
	}
	if len(response.Results) != 3 {
		t.Fatalf("results = %d, want one per path", len(response.Results))
	}
	if response.Results[0].Metadata == nil || !response.Results[0].Metadata.IsFile || response.Results[0].Metadata.Size != 5 {
		t.Fatalf("first result = %+v", response.Results[0])
	}
	if response.Results[1].Error == nil || response.Results[1].Error.Code != -32004 {
		t.Fatalf("missing result = %+v", response.Results[1])
	}
	if response.Results[2].Metadata == nil || !response.Results[2].Metadata.IsDirectory {
		t.Fatalf("directory result = %+v", response.Results[2])
	}
}

// Rust GetMetadataOptions.follow_symlinks: false reports the link itself.
func TestFSGetMetadataBatchFollowSymlinksLikeRust(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs developer mode on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	follow := false
	response, err := getMetadataBatch(&FSGetMetadataBatchParams{
		Paths:          []string{fileURIForMetadataBatchTest(link)},
		FollowSymlinks: &follow,
	})
	if err != nil {
		t.Fatalf("getMetadataBatch() error = %v", err)
	}
	if metadata := response.Results[0].Metadata; metadata == nil || !metadata.IsSymlink || metadata.IsFile {
		t.Fatalf("no-follow result = %+v", response.Results[0])
	}
	response, err = getMetadataBatch(&FSGetMetadataBatchParams{
		Paths: []string{fileURIForMetadataBatchTest(link)},
	})
	if err != nil {
		t.Fatalf("getMetadataBatch() error = %v", err)
	}
	if metadata := response.Results[0].Metadata; metadata == nil || !metadata.IsSymlink || !metadata.IsFile {
		t.Fatalf("follow result = %+v", response.Results[0])
	}
}

// The server dispatches fs/getMetadataBatch and rejects batches over the limit.
func TestServerDispatchesMetadataBatchLikeRust(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(filePath, []byte("hi"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	value, err := NewServer().handleRequest(context.Background(), &request{
		Method: MethodFSGetMetadataBatch,
		Params: mustMarshalRawMessage(t, FSGetMetadataBatchParams{Paths: []string{pathToURI(filePath)}}),
	})
	if err != nil {
		t.Fatalf("handleRequest() error = %v", err)
	}
	response, ok := value.(*FSGetMetadataBatchResponse)
	if !ok {
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatalf("Marshal() error = %v", marshalErr)
		}
		var decoded FSGetMetadataBatchResponse
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("response %T is not a batch: %v", value, err)
		}
		response = &decoded
	}
	if len(response.Results) != 1 || response.Results[0].Metadata == nil || response.Results[0].Metadata.Size != 2 {
		t.Fatalf("batch response = %+v", response)
	}

	paths := make([]string, MaxMetadataBatchProbes+1)
	for index := range paths {
		paths[index] = pathToURI(filePath)
	}
	_, err = NewServer().handleRequest(context.Background(), &request{
		Method: MethodFSGetMetadataBatch,
		Params: mustMarshalRawMessage(t, FSGetMetadataBatchParams{Paths: paths}),
	})
	var failure *requestFailure
	if !errors.As(err, &failure) || failure.code != -32600 || !strings.Contains(failure.message, "exceeds size limit") {
		t.Fatalf("oversized batch error = %#v", err)
	}
}

// The environment advertises the batch capability so remote hosts can pick the
// single-request path.
func TestLocalEnvironmentAdvertisesMetadataBatchLikeRust(t *testing.T) {
	info := localEnvironmentInfo()
	if info == nil || !info.Capabilities.FSMetadataBatch {
		t.Fatalf("capabilities = %+v", info)
	}
	encoded, err := json.Marshal(info.Capabilities)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"fsMetadataBatch":true`) {
		t.Fatalf("capability wire = %s", encoded)
	}
}

func fileURIForMetadataBatchTest(path string) string {
	return pathToURI(path)
}
