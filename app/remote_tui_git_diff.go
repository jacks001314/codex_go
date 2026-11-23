package app

// Rust parity: codex-rs/tui/src/workspace_command.rs and command/exec host
// routing (#63400). `/diff` in a TUI attached to a local daemon runs Git
// through the app server so the commands inherit the session permission
// profile instead of the daemon's defaults.

import (
	"context"
	"errors"
	"strings"
	"time"

	"codex_go/appserver"

	"codex_go/appserverdaemon"
	"codex_go/sandbox"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// remoteTUIDiffReader returns the `/diff` reader for the attached app server.
// Only a local daemon can resolve a session profile for the command's working
// directory; other targets keep the in-process reader.
func remoteTUIDiffReader(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.GitDiffReaderFunc {
	if !interactiveRemoteEndpointIsLocal(endpoint) {
		return nil
	}
	return newRemoteTUIDiffReader(ctx, endpoint)
}

// newRemoteTUIDiffReader builds the `/diff` reader for an app-server-backed
// TUI. Each invocation owns one client for its Git commands, because the TUI
// client's request loop drops replies it is not waiting for.
func newRemoteTUIDiffReader(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.GitDiffReaderFunc {
	return func(cwd string, permissionProfile string) (string, bool, error) {
		runner := &remoteTUIDiffRunner{ctx: ctx, endpoint: endpoint}
		defer runner.close()
		return codextui.ReadGitDiffWithProfile(ctx, runner, cwd, permissionProfile)
	}
}

type remoteTUIDiffRunner struct {
	ctx      context.Context
	endpoint *appserverdaemon.RemoteAppServerEndpoint
	client   *remoteAppServerTUIClient
}

func (r *remoteTUIDiffRunner) sessionClient() (*remoteAppServerTUIClient, error) {
	if r.client != nil {
		return r.client, nil
	}
	if r.endpoint == nil {
		return nil, errors.New("app-server endpoint is unavailable")
	}
	client, err := openRemoteSessionClient(r.ctx, r.endpoint)
	if err != nil {
		return nil, err
	}
	r.client = client
	return client, nil
}

func (r *remoteTUIDiffRunner) close() {
	if r != nil && r.client != nil {
		r.client.close()
		r.client = nil
	}
}

// RunWorkspaceCommand forwards one Git command to `command/exec`. The session
// profile travels with the command; the host rejects combining it with an
// explicit sandbox policy, which /diff never sets.
func (r *remoteTUIDiffRunner) RunWorkspaceCommand(ctx context.Context, command codextui.WorkspaceCommand) (codextui.WorkspaceCommandOutput, error) {
	if err := command.Validate(); err != nil {
		return codextui.WorkspaceCommandOutput{}, err
	}
	client, err := r.sessionClient()
	if err != nil {
		return codextui.WorkspaceCommandOutput{}, err
	}
	params := appserver.CommandExecParams{
		Command:          append([]string(nil), command.Argv...),
		DisableOutputCap: command.DisableOutputCap,
	}
	if cwd := strings.TrimSpace(command.CWD); cwd != "" {
		params.CWD = &cwd
	}
	if len(command.Env) > 0 {
		params.Env = map[string]*string{}
		for key, value := range command.Env {
			params.Env[key] = value
		}
	}
	timeout := command.Timeout
	if timeout <= 0 {
		timeout = codextui.DefaultWorkspaceCommandTimeout
	}
	timeoutMillis := int64(timeout / time.Millisecond)
	params.TimeoutMS = &timeoutMillis
	if profile := strings.TrimSpace(command.PermissionProfile); profile != "" {
		params.PermissionProfile = &profile
	}
	var response appserver.CommandExecResponse
	if err := remoteSessionRequest(ctx, client, appserver.MethodCommandExec, params, &response); err != nil {
		return codextui.WorkspaceCommandOutput{}, err
	}
	return codextui.WorkspaceCommandOutput{
		ExitCode: int(response.ExitCode),
		Stdout:   response.Stdout,
		Stderr:   response.Stderr,
	}, nil
}

// HasPermissionProfile is the reattachment probe: a client that reattached to a
// live thread inherits the profile id but not the flags that defined it, so the
// catalog decides whether the name still resolves.
func (r *remoteTUIDiffRunner) HasPermissionProfile(ctx context.Context, cwd string, profileID string) (bool, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return false, nil
	}
	client, err := r.sessionClient()
	if err != nil {
		return false, err
	}
	const pageLimit = 100
	const maxPages = 10
	seenCursors := map[string]bool{}
	var cursor *string
	for page := 0; page < maxPages; page++ {
		params := sandbox.PermissionProfileListParams{Limit: intPtrValue(pageLimit)}
		if cursor != nil {
			params.Cursor = cursor
		}
		if cwd = strings.TrimSpace(cwd); cwd != "" {
			params.CWD = &cwd
		}
		var listed sandbox.PermissionProfileListResponse
		if err := remoteSessionRequest(ctx, client, appserver.MethodPermissionProfileList, params, &listed); err != nil {
			return false, err
		}
		for _, profile := range listed.Data {
			if strings.TrimSpace(profile.ID) == profileID {
				return true, nil
			}
		}
		if listed.NextCursor == nil || strings.TrimSpace(*listed.NextCursor) == "" {
			return false, nil
		}
		next := strings.TrimSpace(*listed.NextCursor)
		if seenCursors[next] {
			return false, nil
		}
		seenCursors[next] = true
		cursor = &next
	}
	return false, nil
}
