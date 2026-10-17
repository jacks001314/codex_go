package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"codex_go/execserver"
)

// multiEnvCopyFileInvocation builds the function payload a model would send.
func multiEnvCopyFileInvocation(t *testing.T, args map[string]any) *Invocation {
	t.Helper()
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal copy arguments: %v", err)
	}
	return &Invocation{
		CallID:   "call-copy",
		ToolName: PlainName(MultiEnvCopyFileToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: string(encoded)},
	}
}

// multiEnvCopyFileHandlerFor wires the turn's environment filesystems the way
// tool registry options do.
func multiEnvCopyFileHandlerFor(cwd string, environments []UnifiedExecEnvironment) *MultiEnvCopyFileHandler {
	return NewMultiEnvCopyFileHandler(MultiEnvCopyFileOptions{
		EnvironmentFileSystems: NewUnifiedExecEnvironmentFileSystems(environments, cwd),
	})
}

// multiEnvCopyFileModelMessage runs the call and returns the model-visible
// rejection text (Rust `FunctionCallError::RespondToModel`).
func multiEnvCopyFileModelMessage(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("Execute() error = nil, want a model-visible rejection")
	}
	var callError *FunctionCallError
	if !AsFunctionCallError(err, &callError) || !callError.RespondsToModel() {
		t.Fatalf("Execute() error = %v, want a respond-to-model error", err)
	}
	return callError.ModelMessage()
}

func multiEnvCopyFileHostConventionName() string {
	if runtime.GOOS == "windows" {
		return "Windows"
	}
	return "POSIX"
}

func multiEnvCopyFileForeignAbsolutePath() string {
	if runtime.GOOS == "windows" {
		return "/foreign/source.txt"
	}
	return `Z:\foreign\source.txt`
}

// TestMultiEnvCopyFileSpecLikeRust pins the opt-in tool's declaration
// (codex-rs/ext/multi-env-copy-file/src/tool.rs `CopyFileTool::spec`).
func TestMultiEnvCopyFileSpecLikeRust(t *testing.T) {
	handler := NewMultiEnvCopyFileHandler(MultiEnvCopyFileOptions{})
	spec := handler.Spec()
	if spec.Name != PlainName(MultiEnvCopyFileToolName) || spec.Name.Namespace != "" {
		t.Fatalf("spec name = %#v, want the plain name %q", spec.Name, MultiEnvCopyFileToolName)
	}
	if spec.Description != DefaultMultiEnvCopyFileDescription {
		t.Fatalf("spec description = %q", spec.Description)
	}
	if spec.Exposure != "" {
		t.Fatalf("spec exposure = %q, want the default model-visible exposure", spec.Exposure)
	}
	if spec.InputSchema["type"] != "object" {
		t.Fatalf("input schema type = %v", spec.InputSchema["type"])
	}
	if spec.InputSchema["additionalProperties"] != false {
		t.Fatalf("input schema additionalProperties = %v, want false", spec.InputSchema["additionalProperties"])
	}
	properties, ok := spec.InputSchema["properties"].(map[string]any)
	if !ok || len(properties) != 4 {
		t.Fatalf("input schema properties = %#v, want four string properties", spec.InputSchema["properties"])
	}
	wantDescriptions := map[string]string{
		"source_environment_id": "Source environment ID from <environment_context>.",
		"source_path":           "Absolute file path in the source environment. Parent directories must exist.",
		"target_environment_id": "Target environment ID from <environment_context>.",
		"target_path":           "Absolute file path in the target environment. Parent directories must exist.",
	}
	for name, description := range wantDescriptions {
		property, ok := properties[name].(map[string]any)
		if !ok {
			t.Fatalf("property %q = %#v", name, properties[name])
		}
		if property["type"] != "string" || property["description"] != description {
			t.Fatalf("property %q = %#v, want the Rust string schema", name, property)
		}
	}
	required, ok := spec.InputSchema["required"].([]string)
	if !ok {
		t.Fatalf("input schema required = %#v", spec.InputSchema["required"])
	}
	if strings.Join(required, ",") != "source_environment_id,source_path,target_environment_id,target_path" {
		t.Fatalf("required = %v", required)
	}
}

// TestMultiEnvCopyFileCopiesBetweenEnvironmentsLikeRust copies a binary file
// between two selected environments and proves the bytes stay outside the
// model-visible result (codex-rs/ext/multi-env-copy-file/tests/suite/transfers.rs).
func TestMultiEnvCopyFileCopiesBetweenEnvironmentsLikeRust(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "source")
	targetDir := filepath.Join(root, "target")
	for _, dir := range []string{sourceDir, targetDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", dir, err)
		}
	}
	payload := make([]byte, 0, 1_500_000)
	for i := 0; i < 1_500_000; i++ {
		payload = append(payload, byte(i*7%251))
	}
	sourcePath := filepath.Join(sourceDir, "source.bin")
	if err := os.WriteFile(sourcePath, payload, 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	targetPath := filepath.Join(targetDir, "target.bin")
	handler := multiEnvCopyFileHandlerFor(sourceDir, []UnifiedExecEnvironment{{ID: "second", CWD: targetDir}})

	output, err := handler.Execute(context.Background(), multiEnvCopyFileInvocation(t, map[string]any{
		"source_environment_id": "",
		"source_path":           sourcePath,
		"target_environment_id": "second",
		"target_path":           targetPath,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := fmt.Sprintf(`{"bytes_copied":%d}`, len(payload))
	if output.Body != want {
		t.Fatalf("output body = %q, want %q", output.Body, want)
	}
	if !output.Success {
		t.Fatalf("output = %#v, want success", output)
	}
	copied, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read copied target: %v", err)
	}
	if !bytes.Equal(copied, payload) {
		t.Fatalf("copied %d bytes, want %d identical bytes", len(copied), len(payload))
	}

	// A same-environment copy is rejected before the target is opened.
	if _, err := handler.Execute(context.Background(), multiEnvCopyFileInvocation(t, map[string]any{
		"source_environment_id": "second",
		"source_path":           targetPath,
		"target_environment_id": "second",
		"target_path":           filepath.Join(targetDir, "same.bin"),
	})); err != nil {
		message := multiEnvCopyFileModelMessage(t, err)
		if message != "source and target environment IDs must differ. To copy within one environment, use a shell command." {
			t.Fatalf("same-environment message = %q", message)
		}
	} else {
		t.Fatal("same-environment copy = nil error, want a rejection")
	}
	if _, err := os.Stat(filepath.Join(targetDir, "same.bin")); !os.IsNotExist(err) {
		t.Fatalf("same-environment copy touched the target: %v", err)
	}
}

// recordingTargetEnvironmentFileSystem records the target size before every
// pull, so the test can prove the writer finished each block before the source
// was asked for the next one (Rust
// `streaming_replacement_writes_before_reading_the_next_block`).
type recordingTargetEnvironmentFileSystem struct {
	EnvironmentFileSystem
	target string
	sizes  []int64
}

func (f *recordingTargetEnvironmentFileSystem) WriteFileStream(ctx context.Context, path string, contents execserver.FileWriteChunkSource, sandbox *execserver.FileSystemSandboxContext) (int64, error) {
	wrapped := func(ctx context.Context) ([]byte, bool, error) {
		if info, err := os.Stat(f.target); err == nil {
			f.sizes = append(f.sizes, info.Size())
		}
		return contents(ctx)
	}
	return f.EnvironmentFileSystem.WriteFileStream(ctx, path, wrapped, sandbox)
}

type recordingTargetEnvironmentFileSystemProvider struct {
	inner   EnvironmentFileSystemProvider
	environ recordingTargetEnvironmentFileSystem
}

func (p *recordingTargetEnvironmentFileSystemProvider) FileSystemFor(environmentID string) (EnvironmentFileSystem, bool) {
	fileSystem, ok := p.inner.FileSystemFor(environmentID)
	if !ok || fileSystem == nil || environmentID != "second" {
		return fileSystem, ok
	}
	p.environ.EnvironmentFileSystem = fileSystem
	return &p.environ, true
}

// TestMultiEnvCopyFileStreamsBlockByBlockLikeRust drives a three-block copy
// (2 MiB and a tail) and checks the source is read one block at a time.
func TestMultiEnvCopyFileStreamsBlockByBlockLikeRust(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "source")
	targetDir := filepath.Join(root, "target")
	for _, dir := range []string{sourceDir, targetDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", dir, err)
		}
	}
	chunk := execserver.FileReadChunkSize
	payload := append(bytes.Repeat([]byte{0xAB}, 2*chunk), []byte("tail")...)
	sourcePath := filepath.Join(sourceDir, "source.bin")
	if err := os.WriteFile(sourcePath, payload, 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	targetPath := filepath.Join(targetDir, "target.bin")
	environments := []UnifiedExecEnvironment{{ID: "second", CWD: targetDir}}
	provider := &recordingTargetEnvironmentFileSystemProvider{
		inner:   NewUnifiedExecEnvironmentFileSystems(environments, sourceDir),
		environ: recordingTargetEnvironmentFileSystem{target: targetPath},
	}
	handler := NewMultiEnvCopyFileHandler(MultiEnvCopyFileOptions{EnvironmentFileSystems: provider})

	output, err := handler.Execute(context.Background(), multiEnvCopyFileInvocation(t, map[string]any{
		"source_environment_id": "",
		"source_path":           sourcePath,
		"target_environment_id": "second",
		"target_path":           targetPath,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if want := fmt.Sprintf(`{"bytes_copied":%d}`, len(payload)); output.Body != want {
		t.Fatalf("output body = %q, want %q", output.Body, want)
	}
	// The writer pulls once per block and once more for the end of the stream, so
	// the target holds every block already written when the next one is read.
	wantSizes := []int64{0, int64(chunk), int64(2 * chunk), int64(len(payload))}
	if len(provider.environ.sizes) != len(wantSizes) {
		t.Fatalf("target pulls = %v, want %d pulls", provider.environ.sizes, len(wantSizes))
	}
	for i, want := range wantSizes {
		if provider.environ.sizes[i] != want {
			t.Fatalf("target size before pull %d = %d, want %d", i+1, provider.environ.sizes[i], want)
		}
	}
}

// TestMultiEnvCopyFileRejectsInvalidEndpointsLikeRust covers the argument and
// endpoint validation of
// codex-rs/ext/multi-env-copy-file/tests/suite/mod.rs::copy_file_validates_endpoints.
func TestMultiEnvCopyFileRejectsInvalidEndpointsLikeRust(t *testing.T) {
	root := t.TempDir()
	secondDir := filepath.Join(root, "second")
	if err := os.MkdirAll(secondDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(second) error = %v", err)
	}
	sourcePath := filepath.Join(root, "source.txt")
	if err := os.WriteFile(sourcePath, []byte("source"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	handler := multiEnvCopyFileHandlerFor(root, []UnifiedExecEnvironment{{ID: "second", CWD: secondDir}})
	valid := map[string]any{
		"source_environment_id": "",
		"source_path":           sourcePath,
		"target_environment_id": "second",
		"target_path":           filepath.Join(secondDir, "target.txt"),
	}
	withArgument := func(name string, value any) map[string]any {
		args := map[string]any{}
		for key, existing := range valid {
			args[key] = existing
		}
		if value == nil {
			delete(args, name)
		} else {
			args[name] = value
		}
		return args
	}
	cases := []struct {
		name    string
		args    map[string]any
		message string
	}{
		{"missing_source_environment_id", withArgument("source_environment_id", nil), "missing field `source_environment_id`"},
		{"missing_target_path", withArgument("target_path", nil), "missing field `target_path`"},
		{"unknown_source", withArgument("source_environment_id", "unknown"), "unknown or unavailable environment ID: unknown"},
		{"unknown_target", withArgument("target_environment_id", "unknown"), "unknown or unavailable environment ID: unknown"},
		{"relative_source_path", withArgument("source_path", "source.txt"), fmt.Sprintf("provide an absolute %s file path for environment ", multiEnvCopyFileHostConventionName())},
		{"foreign_source_path", withArgument("source_path", multiEnvCopyFileForeignAbsolutePath()), fmt.Sprintf("provide an absolute %s file path for environment ", multiEnvCopyFileHostConventionName())},
		{"unknown_field", map[string]any{
			"source_environment_id": "", "source_path": sourcePath,
			"target_environment_id": "second", "target_path": filepath.Join(secondDir, "target.txt"),
			"extra": 1,
		}, "unknown field"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := handler.Execute(context.Background(), multiEnvCopyFileInvocation(t, testCase.args))
			message := multiEnvCopyFileModelMessage(t, err)
			if !strings.Contains(message, testCase.message) {
				t.Fatalf("message = %q, want it to contain %q", message, testCase.message)
			}
		})
	}

	// A relative environment cwd cannot yield a path convention.
	relativeCWD := multiEnvCopyFileHandlerFor(root, []UnifiedExecEnvironment{{ID: "second", CWD: filepath.Join("relative", "dir")}})
	args := map[string]any{
		"source_environment_id": "",
		"source_path":           sourcePath,
		"target_environment_id": "second",
		"target_path":           filepath.Join(secondDir, "target.txt"),
	}
	_, err := relativeCWD.Execute(context.Background(), multiEnvCopyFileInvocation(t, args))
	if message := multiEnvCopyFileModelMessage(t, err); message != "invalid working directory path for environment second" {
		t.Fatalf("relative cwd message = %q", message)
	}
}

// TestMultiEnvCopyFileReportsErrorKindsLikeRust checks the tool tells the model
// only the error kind, which keeps an executor's connection message (and any
// signed-URL credential in it) out of model context.
func TestMultiEnvCopyFileReportsErrorKindsLikeRust(t *testing.T) {
	root := t.TempDir()
	secondDir := filepath.Join(root, "second")
	if err := os.MkdirAll(secondDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(second) error = %v", err)
	}
	handler := multiEnvCopyFileHandlerFor(root, []UnifiedExecEnvironment{{ID: "second", CWD: secondDir}})
	copyArgs := func(source, target string) map[string]any {
		return map[string]any{
			"source_environment_id": "",
			"source_path":           source,
			"target_environment_id": "second",
			"target_path":           target,
		}
	}

	_, err := handler.Execute(context.Background(), multiEnvCopyFileInvocation(t, copyArgs(filepath.Join(root, "missing.bin"), filepath.Join(secondDir, "target.bin"))))
	if message := multiEnvCopyFileModelMessage(t, err); message != "could not open copy source: entity not found" {
		t.Fatalf("missing source message = %q", message)
	}

	sourcePath := filepath.Join(root, "source.bin")
	if err := os.WriteFile(sourcePath, []byte("payload"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	_, err = handler.Execute(context.Background(), multiEnvCopyFileInvocation(t, copyArgs(root, filepath.Join(secondDir, "target.bin"))))
	message := multiEnvCopyFileModelMessage(t, err)
	if !strings.HasPrefix(message, "could not open copy source: ") {
		t.Fatalf("directory source message = %q", message)
	}
	if !multiEnvCopyFileKindAllowed(strings.TrimPrefix(message, "could not open copy source: "), "invalid input parameter", "permission denied") {
		t.Fatalf("directory source kind from %q", message)
	}

	_, err = handler.Execute(context.Background(), multiEnvCopyFileInvocation(t, copyArgs(sourcePath, secondDir)))
	message = multiEnvCopyFileModelMessage(t, err)
	const prefix = "copy failed; target may be truncated or partially written: "
	if !strings.HasPrefix(message, prefix) {
		t.Fatalf("directory target message = %q", message)
	}
	if !multiEnvCopyFileKindAllowed(strings.TrimPrefix(message, prefix), "is a directory", "permission denied", "invalid input parameter") {
		t.Fatalf("directory target kind from %q", message)
	}
}

func multiEnvCopyFileKindAllowed(kind string, allowed ...string) bool {
	for _, candidate := range allowed {
		if kind == candidate {
			return true
		}
	}
	return false
}

// TestMultiEnvCopyFileRejectsSymlinkSourceLikeRust keeps Rust #39200's
// O_NOFOLLOW rule: a symlink at the final component cannot redirect a copy.
func TestMultiEnvCopyFileRejectsSymlinkSourceLikeRust(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symlink needs privileges on Windows")
	}
	root := t.TempDir()
	secondDir := filepath.Join(root, "second")
	if err := os.MkdirAll(secondDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(second) error = %v", err)
	}
	secret := filepath.Join(root, "secret.bin")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatalf("WriteFile(secret) error = %v", err)
	}
	link := filepath.Join(root, "link.bin")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	handler := multiEnvCopyFileHandlerFor(root, []UnifiedExecEnvironment{{ID: "second", CWD: secondDir}})
	_, err := handler.Execute(context.Background(), multiEnvCopyFileInvocation(t, map[string]any{
		"source_environment_id": "",
		"source_path":           link,
		"target_environment_id": "second",
		"target_path":           filepath.Join(secondDir, "target.bin"),
	}))
	if message := multiEnvCopyFileModelMessage(t, err); message != "could not open copy source: invalid input parameter" {
		t.Fatalf("symlink source message = %q", message)
	}
	if _, err := os.Stat(filepath.Join(secondDir, "target.bin")); !os.IsNotExist(err) {
		t.Fatalf("symlink copy touched the target: %v", err)
	}
}

// TestMultiEnvCopyFileCopiesAcrossAnExecServerLikeRust drives both directions
// against a real exec server, so the remote streaming read and write paths are
// covered end to end.
func TestMultiEnvCopyFileCopiesAcrossAnExecServerLikeRust(t *testing.T) {
	serverURL, stop := startEnvironmentFileSystemExecServer(t)
	defer stop()
	sourceDir := t.TempDir()
	remoteDir := t.TempDir()
	handler := multiEnvCopyFileHandlerFor(sourceDir, []UnifiedExecEnvironment{{
		ID:            "remote-x",
		CWD:           remoteDir,
		ExecServerURL: serverURL,
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	payload := append(bytes.Repeat([]byte{0x11, 0x22, 0x33}, 400_000), 0xFF)
	localSource := filepath.Join(sourceDir, "source.bin")
	if err := os.WriteFile(localSource, payload, 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	remoteTarget := filepath.Join(remoteDir, "target.bin")
	output, err := handler.Execute(ctx, multiEnvCopyFileInvocation(t, map[string]any{
		"source_environment_id": "",
		"source_path":           localSource,
		"target_environment_id": "remote-x",
		"target_path":           remoteTarget,
	}))
	if err != nil {
		t.Fatalf("outbound Execute() error = %v", err)
	}
	if want := fmt.Sprintf(`{"bytes_copied":%d}`, len(payload)); output.Body != want {
		t.Fatalf("outbound body = %q, want %q", output.Body, want)
	}
	copied, err := os.ReadFile(remoteTarget)
	if err != nil {
		t.Fatalf("read remote target: %v", err)
	}
	if !bytes.Equal(copied, payload) {
		t.Fatalf("remote target = %d bytes, want %d identical bytes", len(copied), len(payload))
	}

	localReturn := filepath.Join(sourceDir, "returned.bin")
	output, err = handler.Execute(ctx, multiEnvCopyFileInvocation(t, map[string]any{
		"source_environment_id": "remote-x",
		"source_path":           remoteTarget,
		"target_environment_id": "",
		"target_path":           localReturn,
	}))
	if err != nil {
		t.Fatalf("inbound Execute() error = %v", err)
	}
	if want := fmt.Sprintf(`{"bytes_copied":%d}`, len(payload)); output.Body != want {
		t.Fatalf("inbound body = %q, want %q", output.Body, want)
	}
	returned, err := os.ReadFile(localReturn)
	if err != nil {
		t.Fatalf("read returned file: %v", err)
	}
	if !bytes.Equal(returned, payload) {
		t.Fatalf("returned file = %d bytes, want %d identical bytes", len(returned), len(payload))
	}

	// The executor never creates parent directories: a missing parent is an I/O
	// failure on the write, not a silent mkdir (Rust
	// `copy_file_validates_endpoints`, `<CWD>/missing/target.txt`).
	_, err = handler.Execute(ctx, multiEnvCopyFileInvocation(t, map[string]any{
		"source_environment_id": "",
		"source_path":           localSource,
		"target_environment_id": "remote-x",
		"target_path":           filepath.Join(remoteDir, "missing", "target.bin"),
	}))
	if message := multiEnvCopyFileModelMessage(t, err); message != "copy failed; target may be truncated or partially written: entity not found" {
		t.Fatalf("missing target parent message = %q", message)
	}
}
