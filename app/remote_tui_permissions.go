package app

import (
	"context"
	"errors"
	"strings"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/sandbox"
	chatwidget "codex_go/tui/chatwidget"
	codextea "codex_go/tui/tea"
)

// remoteTUIListPermissionProfiles discovers the connected app server's named
// permission profiles, mirroring permission_discovery::fetch's bounded
// pagination and duplicate check (Rust #43340).
func remoteTUIListPermissionProfiles(ctx context.Context, client *remoteAppServerTUIClient) ([]chatwidget.CustomPermissionProfile, error) {
	if client == nil {
		return nil, errors.New("app-server client is unavailable")
	}
	const pageLimit = 100
	const maxPages = 10
	profiles := []chatwidget.CustomPermissionProfile{}
	seenIDs := map[string]bool{}
	seenCursors := map[string]bool{}
	var cursor *string
	for page := 0; page < maxPages; page++ {
		params := sandbox.PermissionProfileListParams{Limit: intPtrValue(pageLimit)}
		if cursor != nil {
			params.Cursor = cursor
		}
		if cwd := strings.TrimSpace(client.state.CWD); cwd != "" {
			params.CWD = &cwd
		}
		var listed sandbox.PermissionProfileListResponse
		if err := remoteSessionRequest(ctx, client, appserver.MethodPermissionProfileList, params, &listed); err != nil {
			if remoteTUIPermissionDiscoveryUnsupported(err) {
				return nil, codextea.ErrNamedPermissionProfilesUnsupported
			}
			return nil, err
		}
		if len(listed.Data) > pageLimit {
			break
		}
		for _, profile := range listed.Data {
			id := strings.TrimSpace(profile.ID)
			if id == "" {
				continue
			}
			if seenIDs[id] {
				return nil, errors.New("The server returned duplicate permission profiles.")
			}
			seenIDs[id] = true
			profiles = append(profiles, chatwidget.CustomPermissionProfile{
				ID:          id,
				Description: strings.TrimSpace(profile.Description),
				Allowed:     profile.Allowed,
			})
		}
		if listed.NextCursor == nil || strings.TrimSpace(*listed.NextCursor) == "" {
			return profiles, nil
		}
		next := strings.TrimSpace(*listed.NextCursor)
		if seenCursors[next] {
			break
		}
		seenCursors[next] = true
		cursor = &next
	}
	return nil, errors.New("Permission discovery exceeded its pagination limit. Try /permissions again.")
}

// remoteTUIPermissionDiscoveryUnsupported mirrors discovery_error: an older
// server that lacks the discovery RPCs.
func remoteTUIPermissionDiscoveryUnsupported(err error) bool {
	if err == nil {
		return false
	}
	var rpc *remoteRPCError
	if errors.As(err, &rpc) {
		if rpc.Code == -32601 {
			return true
		}
		if rpc.Code == -32600 && (strings.Contains(rpc.Message, "permissionProfile/list") ||
			strings.Contains(rpc.Message, "configRequirements/read") ||
			strings.Contains(rpc.Message, "config/read")) {
			return true
		}
	}
	return false
}

// remoteTUIPermissionUpdateUnsupported mirrors is_thread_settings_update_unsupported.
func remoteTUIPermissionUpdateUnsupported(err error) bool {
	if err == nil {
		return false
	}
	var rpc *remoteRPCError
	if errors.As(err, &rpc) {
		if rpc.Code == -32601 {
			return true
		}
		if rpc.Code == -32600 && strings.Contains(rpc.Message, "thread/settings/update") {
			return true
		}
	}
	return false
}

// interactiveRemoteListPermissionProfiles loads the server's named permission
// profiles for the /permissions picker.
func interactiveRemoteListPermissionProfiles(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) func() ([]chatwidget.CustomPermissionProfile, error) {
	return func() ([]chatwidget.CustomPermissionProfile, error) {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		defer client.close()
		return remoteTUIListPermissionProfiles(ctx, client)
	}
}

// interactiveRemoteUpdateThreadPermissions asks the server to adopt a named
// permission profile through thread/settings/update (Rust #43340).
func interactiveRemoteUpdateThreadPermissions(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) func(threadID string, profileID string) error {
	return func(threadID string, profileID string) error {
		threadID = strings.TrimSpace(threadID)
		profileID = strings.TrimSpace(profileID)
		if threadID == "" || profileID == "" {
			return errors.New("a thread id and permission profile are required")
		}
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return err
		}
		defer client.close()
		var updated appserver.SettingsUpdateResponse
		if err := remoteSessionRequest(ctx, client, appserver.MethodThreadSettingsUpdate, appserver.SettingsUpdateParams{
			ThreadID:    threadID,
			Permissions: &profileID,
		}, &updated); err != nil {
			if remoteTUIPermissionUpdateUnsupported(err) {
				return codextea.ErrNamedPermissionProfilesUnsupported
			}
			return err
		}
		return nil
	}
}

func intPtrValue(value int) *int { return &value }
