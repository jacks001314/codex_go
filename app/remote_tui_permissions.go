package app

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"time"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/config"
	"codex_go/sandbox"
	chatwidget "codex_go/tui/chatwidget"
	codextea "codex_go/tui/tea"
)

// remoteTUIpermissionDiscoveryTimeout mirrors Rust's 10-second permission
// discovery budget (#43340). It is a variable so tests can shrink the budget.
var remoteTUIpermissionDiscoveryTimeout = 10 * time.Second

// remoteTUIListPermissionProfiles discovers the connected app server's named
// permission profiles, mirroring permission_discovery::fetch's bounded
// pagination and duplicate check (Rust #43340). The boolean reports the
// server's explicit-profile mode: a remote thread without an active named
// profile only offers server profiles when the effective config declares a
// string default_permissions, otherwise discovery yields the local presets.
func remoteTUIListPermissionProfiles(ctx context.Context, client *remoteAppServerTUIClient) ([]chatwidget.CustomPermissionProfile, bool, error) {
	if client == nil {
		return nil, false, errors.New("app-server client is unavailable")
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, remoteTUIpermissionDiscoveryTimeout)
	defer cancel()
	profiles, explicitProfileMode, err := remoteTUIPermissionDiscovery(discoveryCtx, client)
	if remoteTUIPermissionDiscoveryTimedOut(discoveryCtx, err) {
		return nil, false, errors.New("Permission discovery timed out. Try /permissions again.")
	}
	return profiles, explicitProfileMode, err
}

// remoteTUIPermissionDiscoveryTimedOut reports whether the bounded discovery
// budget elapsed. The transport read deadline can surface a bare net timeout
// before the context observes its own deadline, so both are treated as the
// timeout Rust's tokio::time::timeout produces.
func remoteTUIPermissionDiscoveryTimedOut(ctx context.Context, err error) bool {
	if ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func remoteTUIPermissionDiscovery(ctx context.Context, client *remoteAppServerTUIClient) ([]chatwidget.CustomPermissionProfile, bool, error) {
	cwd := ""
	if client.state != nil {
		cwd = strings.TrimSpace(client.state.CWD)
	}
	// Rust permission_discovery::fetch: the daemon's catalog cannot see this
	// invocation's profiles, so a remote thread without an active named profile
	// only uses server discovery when the effective config sets
	// default_permissions.
	if !remoteTUIHasActiveNamedProfile(client) {
		params := config.ConfigReadParams{}
		if cwd != "" {
			params.CWD = &cwd
		}
		var read config.ConfigReadResponse
		if err := remoteSessionRequest(ctx, client, appserver.MethodConfigRead, params, &read); err != nil {
			if remoteTUIPermissionDiscoveryUnsupported(err) {
				return nil, false, codextea.ErrNamedPermissionProfilesUnsupported
			}
			return nil, false, err
		}
		if !remoteTUIExplicitPermissionProfileConfig(read.Config) {
			return []chatwidget.CustomPermissionProfile{}, false, nil
		}
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
		if cwd != "" {
			params.CWD = &cwd
		}
		var listed sandbox.PermissionProfileListResponse
		if err := remoteSessionRequest(ctx, client, appserver.MethodPermissionProfileList, params, &listed); err != nil {
			if remoteTUIPermissionDiscoveryUnsupported(err) {
				return nil, false, codextea.ErrNamedPermissionProfilesUnsupported
			}
			return nil, false, err
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
				return nil, false, errors.New("The server returned duplicate permission profiles.")
			}
			seenIDs[id] = true
			profiles = append(profiles, chatwidget.CustomPermissionProfile{
				ID:          id,
				Description: strings.TrimSpace(profile.Description),
				Allowed:     profile.Allowed,
			})
		}
		if listed.NextCursor == nil || strings.TrimSpace(*listed.NextCursor) == "" {
			return profiles, true, nil
		}
		next := strings.TrimSpace(*listed.NextCursor)
		if seenCursors[next] {
			break
		}
		seenCursors[next] = true
		cursor = &next
	}
	return nil, false, errors.New("Permission discovery exceeded its pagination limit. Try /permissions again.")
}

// remoteTUIExplicitPermissionProfileConfig mirrors Rust
// Config::explicit_permission_profile_mode: an explicit default profile
// (`default_permissions`) or the profiles config syntax (a `permissions` table
// defining profiles) puts the server in explicit-profile mode, so a remote
// thread discovers its named profiles instead of falling back to presets.
func remoteTUIExplicitPermissionProfileConfig(configValues map[string]any) bool {
	if _, ok := configValues["default_permissions"].(string); ok {
		return true
	}
	permissions, ok := configValues["permissions"].(map[string]any)
	if !ok {
		return false
	}
	for name, value := range permissions {
		if strings.TrimSpace(name) == "default" {
			continue
		}
		if _, isProfile := value.(map[string]any); isProfile {
			return true
		}
	}
	return false
}

// remoteTUIHasActiveNamedProfile mirrors Rust's
// permissions.active_permission_profile() check for the discovery gate.
func remoteTUIHasActiveNamedProfile(client *remoteAppServerTUIClient) bool {
	if client == nil || client.state == nil {
		return false
	}
	profileID := strings.TrimSpace(client.state.Sandbox)
	return profileID != "" && !strings.HasPrefix(profileID, ":")
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
func interactiveRemoteListPermissionProfiles(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) func() ([]chatwidget.CustomPermissionProfile, bool, error) {
	return func() ([]chatwidget.CustomPermissionProfile, bool, error) {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return nil, false, err
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
