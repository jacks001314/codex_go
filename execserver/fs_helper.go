package execserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"codex_go/sandbox"
	"codex_go/sandbox/windowssandbox"
)

const FSHelperArg1 = "--codex-run-as-fs-helper"

var fsHelperCommandForExecutable = func(executable string) []string {
	return []string{executable, FSHelperArg1}
}

func windowsSandboxProxySettingsModeForFS(mode WindowsSandboxProxySettingsMode) windowssandbox.ProxySettingsMode {
	if mode == WindowsSandboxProxySettingsPreserve {
		return windowssandbox.ProxySettingsPreserve
	}
	return windowssandbox.ProxySettingsReconcile
}

type fsHelperRequest struct {
	Operation string          `json:"operation"`
	Params    json.RawMessage `json:"params"`
}

type fsHelperPayload struct {
	Operation string          `json:"operation"`
	Response  json.RawMessage `json:"response"`
}

type fsHelperResponse struct {
	Status  string          `json:"status"`
	Payload json.RawMessage `json:"payload"`
}

type fsHelperError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func RunFSHelper(stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	input, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "fs sandbox helper failed: %v\n", err)
		return 1
	}
	var request fsHelperRequest
	if err := json.Unmarshal(input, &request); err != nil {
		fmt.Fprintf(stderr, "fs sandbox helper failed: %v\n", err)
		return 1
	}
	response, operationErr := runDirectFSHelperRequest(&request)
	var envelope any
	if operationErr != nil {
		failure := fsOperationFailure(operationErr)
		envelope = struct {
			Status  string        `json:"status"`
			Payload fsHelperError `json:"payload"`
		}{Status: "error", Payload: fsHelperError{Code: failure.code, Message: failure.message}}
	} else {
		envelope = struct {
			Status  string          `json:"status"`
			Payload fsHelperPayload `json:"payload"`
		}{Status: "ok", Payload: fsHelperPayload{Operation: request.Operation, Response: response}}
	}
	if err := json.NewEncoder(stdout).Encode(envelope); err != nil {
		fmt.Fprintf(stderr, "fs sandbox helper failed: %v\n", err)
		return 1
	}
	return 0
}

func runDirectFSHelperRequest(request *fsHelperRequest) (json.RawMessage, error) {
	if request == nil {
		return nil, errors.New("fs helper request is required")
	}
	marshalResponse := func(value any, err error) (json.RawMessage, error) {
		if err != nil {
			return nil, err
		}
		data, marshalErr := json.Marshal(value)
		return data, marshalErr
	}
	switch request.Operation {
	case MethodFSReadFile:
		var params FSReadFileParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		params.Sandbox = nil
		response, err := readFile(&params)
		return marshalResponse(response, err)
	case MethodFSWriteFile:
		var params FSWriteFileParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		params.Sandbox = nil
		response, err := writeFile(&params)
		return marshalResponse(response, err)
	case MethodFSCreateDirectory:
		var params FSCreateDirectoryParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		params.Sandbox = nil
		response, err := createDirectory(&params)
		return marshalResponse(response, err)
	case MethodFSGetMetadata:
		var params FSGetMetadataParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		params.Sandbox = nil
		response, err := getMetadata(&params)
		return marshalResponse(response, err)
	case MethodFSCanonicalize:
		var params FSCanonicalizeParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		params.Sandbox = nil
		response, err := canonicalize(&params)
		return marshalResponse(response, err)
	case MethodFSReadDirectory:
		var params FSReadDirectoryParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		params.Sandbox = nil
		response, err := readDirectory(&params)
		return marshalResponse(response, err)
	case MethodFSWalk:
		var params FSWalkParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		params.Sandbox = nil
		response, err := walkPath(&params)
		return marshalResponse(response, err)
	case MethodFSRemove:
		var params FSRemoveParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		params.Sandbox = nil
		response, err := removePath(&params)
		return marshalResponse(response, err)
	case MethodFSCopy:
		var params FSCopyParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		params.Sandbox = nil
		response, err := copyPath(&params)
		return marshalResponse(response, err)
	default:
		return nil, requestError(-32602, fmt.Sprintf("unknown fs helper operation %s", request.Operation))
	}
}

func runSandboxedFSOperation(ctx *FileSystemSandboxContext, operation string, params any, target any) (bool, error) {
	required, profile, profileJSON, cwd, workspaceRoots, err := prepareFSSandbox(ctx)
	if err != nil || !required {
		return required, err
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return true, err
	}
	var paramsObject map[string]any
	if err := json.Unmarshal(paramsJSON, &paramsObject); err != nil {
		return true, err
	}
	paramsObject["sandbox"] = nil
	paramsJSON, err = json.Marshal(paramsObject)
	if err != nil {
		return true, err
	}
	requestJSON, err := json.Marshal(fsHelperRequest{Operation: operation, Params: paramsJSON})
	if err != nil {
		return true, err
	}
	command, env, err := fsSandboxCommand(ctx, profile, profileJSON, cwd, workspaceRoots)
	if err != nil {
		return true, err
	}
	commandCtx := context.Background()
	cmd := exec.CommandContext(commandCtx, command[0], command[1:]...)
	cmd.Dir = cwd
	cmd.Env = envPairs(env)
	cmd.Stdin = bytes.NewReader(requestJSON)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return true, requestError(-32603, fmt.Sprintf("fs sandbox helper failed with status %v: %s", err, strings.TrimSpace(stderr.String())))
	}
	var response fsHelperResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return true, requestError(-32603, fmt.Sprintf("invalid fs sandbox helper response: %v", err))
	}
	if response.Status == "error" {
		var helperErr fsHelperError
		if err := json.Unmarshal(response.Payload, &helperErr); err != nil {
			return true, requestError(-32603, fmt.Sprintf("invalid fs sandbox helper error response: %v", err))
		}
		return true, requestError(helperErr.Code, helperErr.Message)
	}
	if response.Status != "ok" {
		return true, requestError(-32603, fmt.Sprintf("invalid fs sandbox helper status %q", response.Status))
	}
	var payload fsHelperPayload
	if err := json.Unmarshal(response.Payload, &payload); err != nil {
		return true, requestError(-32603, fmt.Sprintf("invalid fs sandbox helper payload: %v", err))
	}
	if payload.Operation != operation {
		return true, requestError(-32603, fmt.Sprintf("unexpected fs sandbox helper response: expected %s, got %s", operation, payload.Operation))
	}
	if target != nil {
		if err := json.Unmarshal(payload.Response, target); err != nil {
			return true, requestError(-32603, fmt.Sprintf("invalid fs sandbox helper operation response: %v", err))
		}
	}
	return true, nil
}

func prepareFSSandbox(ctx *FileSystemSandboxContext) (bool, *sandbox.PermissionProfile, string, string, []string, error) {
	if ctx == nil {
		return false, nil, "", "", nil, nil
	}
	if !hasJSONValue(ctx.Permissions) {
		return true, nil, "", "", nil, requestError(-32602, "file system sandbox context permissions are required")
	}
	profileJSON, err := nativePermissionProfileJSON(ctx.Permissions)
	if err != nil {
		return true, nil, "", "", nil, requestError(-32602, fmt.Sprintf("invalid sandbox permission path URI: %v", err))
	}
	profile, err := sandbox.ParseRuntimePermissionProfileJSON(profileJSON)
	if err != nil {
		return true, nil, "", "", nil, requestError(-32602, fmt.Sprintf("invalid sandbox permission profile: %v", err))
	}
	if profile.Disabled || profile.LegacySandboxPolicy().HasFullDiskWriteAccess() {
		return false, profile, profileJSON, "", nil, nil
	}
	cwd := ""
	if strings.TrimSpace(ctx.CWD) != "" {
		cwd, err = nativeExecServerPath(ctx.CWD, "file system sandbox cwd")
		if err != nil {
			return true, nil, "", "", nil, err
		}
	} else {
		if fsPermissionsDependOnCWD(ctx.Permissions) {
			return true, nil, "", "", nil, requestError(-32602, "file system sandbox context with dynamic permissions requires cwd")
		}
		cwd, err = os.Getwd()
		if err != nil {
			return true, nil, "", "", nil, err
		}
	}
	if !filepath.IsAbs(cwd) {
		return true, nil, "", "", nil, requestError(-32602, fmt.Sprintf("current directory is not absolute: %s", cwd))
	}
	workspaceRoots := make([]string, 0, len(ctx.WorkspaceRoots))
	for _, root := range ctx.WorkspaceRoots {
		native, pathErr := nativeExecServerPath(root, "workspace root")
		if pathErr != nil {
			return true, nil, "", "", nil, pathErr
		}
		workspaceRoots = append(workspaceRoots, native)
	}
	if len(workspaceRoots) == 0 {
		workspaceRoots = []string{cwd}
	}
	return true, profile, profileJSON, cwd, workspaceRoots, nil
}

func fsPermissionsDependOnCWD(raw json.RawMessage) bool {
	var wire struct {
		Type       string `json:"type"`
		FileSystem struct {
			Type    string `json:"type"`
			Entries []struct {
				Path struct {
					Type    string `json:"type"`
					Pattern string `json:"pattern"`
					Value   struct {
						Kind string `json:"kind"`
					} `json:"value"`
				} `json:"path"`
			} `json:"entries"`
		} `json:"file_system"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.Type != "managed" || wire.FileSystem.Type != "restricted" {
		return false
	}
	for _, entry := range wire.FileSystem.Entries {
		if entry.Path.Type == "glob_pattern" && !filepath.IsAbs(entry.Path.Pattern) {
			return true
		}
		if entry.Path.Type == "special" && entry.Path.Value.Kind == "project_roots" {
			return true
		}
	}
	return false
}

func fsSandboxCommand(ctx *FileSystemSandboxContext, profile *sandbox.PermissionProfile, profileJSON string, cwd string, workspaceRoots []string) ([]string, map[string]string, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return nil, nil, err
	}
	env := fsHelperEnvironment()
	helperCommand := fsHelperCommandForExecutable(executable)
	plan, err := sandbox.BuildCommandRunPlan(&sandbox.CommandRunRequest{
		ResolvedPermissionProfile:     profile,
		ResolvedPermissionProfileID:   "exec-server-fs-helper",
		ResolvedPermissionProfileJSON: profileJSON,
		CWD:                           cwd,
		SandboxReadableRoots:          []string{filepath.Dir(executable)},
		UseLegacyLandlock:             ctx.UseLegacyLandlock,
		Command:                       helperCommand,
	})
	if err != nil {
		return nil, nil, requestError(-32602, fmt.Sprintf("failed to prepare fs sandbox: %v", err))
	}
	if err := plan.UnsupportedError(); err != nil {
		return nil, nil, requestError(-32602, fmt.Sprintf("failed to prepare fs sandbox: %v", err))
	}
	if runtime.GOOS != "windows" {
		return plan.Command, env, nil
	}
	level, err := windowssandbox.ParseWindowsSandboxLevel(ctx.WindowsSandboxLevel)
	if err != nil {
		return nil, nil, requestError(-32602, fmt.Sprintf("failed to prepare fs sandbox: %v", err))
	}
	if level == windowssandbox.WindowsSandboxLevelDisabled {
		return helperCommand, env, nil
	}
	args, err := windowssandbox.CreateWindowsSandboxCommandArgsForPermissionProfile(windowssandbox.WindowsSandboxCommandArgsRequest{
		Command:                          helperCommand,
		CommandCWD:                       cwd,
		WorkspaceRoots:                   workspaceRoots,
		Env:                              env,
		PermissionProfile:                profile,
		WindowsSandboxLevel:              level,
		WindowsSandboxPrivateDesktop:     ctx.WindowsSandboxPrivateDesktop,
		ProxySettingsMode:                windowsSandboxProxySettingsModeForFS(ctx.WindowsSandboxProxySettingsMode),
		ReadRootsIncludePlatformDefaults: true,
		CodexHome:                        fsHelperCodexHome(),
	})
	if err != nil {
		return nil, nil, requestError(-32602, fmt.Sprintf("failed to prepare fs sandbox: %v", err))
	}
	return append([]string{executable}, args...), env, nil
}

func fsHelperEnvironment() map[string]string {
	env := map[string]string{}
	for _, key := range []string{"PATH", "TMPDIR", "TMP", "TEMP"} {
		if value, ok := os.LookupEnv(key); ok {
			env[key] = value
		}
	}
	return env
}

func fsHelperCodexHome() string {
	if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" && filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	home, err := os.UserHomeDir()
	if err == nil && filepath.IsAbs(home) {
		return filepath.Join(home, ".codex")
	}
	return filepath.Join(filepath.VolumeName(os.TempDir())+string(filepath.Separator), ".codex")
}

func fsOperationFailure(err error) *requestFailure {
	var failure *requestFailure
	if errors.As(err, &failure) {
		return failure
	}
	switch {
	case os.IsNotExist(err):
		return &requestFailure{code: -32004, message: err.Error()}
	case os.IsPermission(err), errors.Is(err, os.ErrInvalid):
		return &requestFailure{code: -32600, message: err.Error()}
	default:
		return &requestFailure{code: -32603, message: err.Error()}
	}
}

func mapFSRequestError(err error) error {
	if err == nil {
		return nil
	}
	return fsOperationFailure(err)
}
