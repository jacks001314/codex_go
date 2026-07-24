//go:build windows

package win

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"codex_go/sandbox/windowssandbox"
	json "github.com/goccy/go-json"
	"golang.org/x/sys/windows"
)

const (
	setupModeFull         setupMode = "full"
	setupModeProvision    setupMode = "provision-only"
	setupModeReadACLsOnly setupMode = "read-acls-only"

	createNoWindow       = 0x08000000
	setupFileDeleteChild = 0x00000040
)

type setupMode string

type setupPayload struct {
	Version           int             `json:"version"`
	OfflineUsername   string          `json:"offline_username"`
	OnlineUsername    string          `json:"online_username"`
	CodexHome         string          `json:"codex_home"`
	CommandCWD        string          `json:"command_cwd"`
	ReadRoots         []string        `json:"read_roots"`
	WriteRoots        []string        `json:"write_roots"`
	DenyReadPaths     []string        `json:"deny_read_paths,omitempty"`
	DenyWritePaths    []string        `json:"deny_write_paths,omitempty"`
	ProxyPorts        []uint16        `json:"proxy_ports"`
	AllowLocalBinding bool            `json:"allow_local_binding,omitempty"`
	Otel              json.RawMessage `json:"otel,omitempty"`
	RealUser          string          `json:"real_user"`
	Mode              setupMode       `json:"mode,omitempty"`
	RefreshOnly       bool            `json:"refresh_only,omitempty"`
}

func run(args []string, stdout io.Writer, stderr io.Writer) error {
	payload, err := payloadFromArgs(args)
	if err != nil {
		return err
	}
	sbzDir := windowssandbox.SandboxDir(payload.CodexHome)
	if err := os.MkdirAll(sbzDir, 0o700); err != nil {
		return setupFailuref(
			windowssandbox.SetupErrorHelperSandboxDirCreateFailed,
			"failed to create sandbox dir %s: %v", sbzDir, err,
		)
	}
	log, err := openSetupLog(sbzDir)
	if err != nil {
		return setupFailuref(
			windowssandbox.SetupErrorHelperLogFailed,
			"open log in %s failed: %v", sbzDir, err,
		)
	}
	defer log.Close()

	err = runSetupPayload(payload, log, sbzDir)
	if err != nil {
		setupLogLine(log, fmt.Sprintf("setup error: %v", err))
		_ = windowssandbox.LogNoteInDir(sbzDir, fmt.Sprintf("setup error: %v", err))
		failure := extractSetupFailure(err)
		if failure == nil {
			failure = windowssandbox.NewSetupFailure(windowssandbox.SetupErrorHelperUnknownError, err.Error())
		}
		report := &windowssandbox.SetupErrorReport{Code: failure.Code, Message: failure.Message}
		if writeErr := windowssandbox.WriteSetupErrorReport(payload.CodexHome, report); writeErr != nil {
			setupLogLine(log, fmt.Sprintf("setup error report write failed: %v", writeErr))
			_ = windowssandbox.LogNoteInDir(sbzDir, fmt.Sprintf("setup error report write failed: %v", writeErr))
		}
		return err
	}
	return nil
}

func payloadFromArgs(args []string) (*setupPayload, error) {
	if len(args) != 1 {
		return nil, setupFailuref(windowssandbox.SetupErrorHelperRequestArgsFailed, "expected payload argument")
	}
	payloadJSON, err := base64.StdEncoding.DecodeString(args[0])
	if err != nil {
		return nil, setupFailuref(windowssandbox.SetupErrorHelperRequestArgsFailed, "failed to decode payload b64: %v", err)
	}
	var payload setupPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return nil, setupFailuref(windowssandbox.SetupErrorHelperRequestArgsFailed, "failed to parse payload json: %v", err)
	}
	if payload.Version != windowssandbox.SetupVersion {
		return nil, setupFailuref(
			windowssandbox.SetupErrorHelperRequestArgsFailed,
			"setup version mismatch: expected %d, got %d",
			windowssandbox.SetupVersion,
			payload.Version,
		)
	}
	if payload.Mode == "" {
		payload.Mode = setupModeFull
	}
	switch payload.Mode {
	case setupModeFull, setupModeProvision, setupModeReadACLsOnly:
	default:
		return nil, setupFailuref(windowssandbox.SetupErrorHelperRequestArgsFailed, "unknown setup mode: %s", payload.Mode)
	}
	if strings.TrimSpace(payload.CodexHome) == "" {
		return nil, setupFailuref(windowssandbox.SetupErrorHelperRequestArgsFailed, "codex_home is required")
	}
	return &payload, nil
}

func runSetupPayload(payload *setupPayload, log io.Writer, sbxDir string) error {
	writesSetupMarker := !payload.RefreshOnly && payload.Mode != setupModeReadACLsOnly
	if writesSetupMarker {
		if err := prepareSetupMarker(payload.CodexHome, payload.RealUser); err != nil {
			return err
		}
	}
	switch payload.Mode {
	case setupModeReadACLsOnly:
		if err := runReadACLOnly(payload, log); err != nil {
			return err
		}
	case setupModeProvision:
		if err := runProvisionOnly(payload, log, sbxDir); err != nil {
			return err
		}
	case setupModeFull:
		if err := runSetupFull(payload, log, sbxDir); err != nil {
			return err
		}
	default:
		return setupFailuref(windowssandbox.SetupErrorHelperRequestArgsFailed, "unknown setup mode: %s", payload.Mode)
	}
	if writesSetupMarker {
		if err := commitSetupMarker(payload.CodexHome, payload.OfflineUsername, payload.OnlineUsername, payload.ProxyPorts, payload.AllowLocalBinding); err != nil {
			return setupFailuref(windowssandbox.SetupErrorHelperSetupMarkerWriteFailed, "commit setup marker failed: %v", err)
		}
	}
	return nil
}

func runReadACLOnly(payload *setupPayload, log io.Writer) error {
	guard, acquired, err := AcquireReadACLMutex()
	if err != nil {
		return err
	}
	if !acquired {
		setupLogLine(log, "read ACL helper already running; skipping")
		return nil
	}
	defer guard.Close()

	setupLogLine(log, "read-acl-only mode: applying read ACLs")
	sandboxGroupSIDBytes, err := ResolveSandboxUsersGroupSID()
	if err != nil {
		return err
	}
	sandboxGroupSID := windowssandbox.StringFromSIDBytes(sandboxGroupSIDBytes)
	refreshErrors := []string{}
	if len(payload.ReadRoots) > 0 {
		usersSID, err := resolveSIDString("Users")
		if err != nil {
			return err
		}
		authSID, err := resolveSIDString("Authenticated Users")
		if err != nil {
			return err
		}
		everyoneSID, err := resolveSIDString("Everyone")
		if err != nil {
			return err
		}
		subjects := readACLSubjects{SandboxGroupSID: sandboxGroupSID, RXSubjectSIDs: []string{usersSID, authSID, everyoneSID}}
		applyReadACLs(payload.ReadRoots, subjects, log, &refreshErrors, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_EXECUTE, "read", windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE)
	}
	if len(refreshErrors) > 0 {
		setupLogLine(log, fmt.Sprintf("read ACL run completed with errors: %v", refreshErrors))
		if payload.RefreshOnly {
			return fmt.Errorf("read ACL run had errors")
		}
	}
	setupLogLine(log, "read ACL run completed")
	return nil
}

func runProvisionOnly(payload *setupPayload, log io.Writer, sbxDir string) error {
	if err := provisionAndHideSandboxUsers(payload, log, sbxDir); err != nil {
		return err
	}
	offlineSID, err := resolveSIDString(payload.OfflineUsername)
	if err != nil {
		return setupFailuref(windowssandbox.SetupErrorHelperSIDResolveFailed, "resolve SID for offline user %s failed: %v", payload.OfflineUsername, err)
	}
	sandboxGroupSIDBytes, err := ResolveSandboxUsersGroupSID()
	if err != nil {
		return setupFailuref(windowssandbox.SetupErrorHelperSIDResolveFailed, "resolve sandbox users group SID failed: %v", err)
	}
	if err := configureOfflineSandboxNetwork(payload, offlineSID, log); err != nil {
		return err
	}
	if err := lockSandboxBinDir(payload, sandboxGroupSIDBytes, log); err != nil {
		return err
	}
	if err := lockPersistentSandboxDirs(payload, sandboxGroupSIDBytes, log); err != nil {
		return err
	}
	_ = windowssandbox.LogNoteInDir(sbxDir, "setup provisioning binary completed")
	return nil
}

func runSetupFull(payload *setupPayload, log io.Writer, sbxDir string) error {
	if !payload.RefreshOnly {
		if err := provisionAndHideSandboxUsers(payload, log, sbxDir); err != nil {
			return err
		}
	}
	offlineSID, err := resolveSIDString(payload.OfflineUsername)
	if err != nil {
		return setupFailuref(windowssandbox.SetupErrorHelperSIDResolveFailed, "resolve SID for offline user %s failed: %v", payload.OfflineUsername, err)
	}
	sandboxGroupSIDBytes, err := ResolveSandboxUsersGroupSID()
	if err != nil {
		return setupFailuref(windowssandbox.SetupErrorHelperSIDResolveFailed, "resolve sandbox users group SID failed: %v", err)
	}
	sandboxGroupSID := windowssandbox.StringFromSIDBytes(sandboxGroupSIDBytes)
	refreshErrors := []string{}

	if !payload.RefreshOnly {
		if err := configureOfflineSandboxNetwork(payload, offlineSID, log); err != nil {
			return err
		}
	}
	appliedDenyReadPaths, err := windowssandbox.SyncPersistentDenyReadACLs(payload.CodexHome, payload.DenyReadPaths, sandboxGroupSID)
	if err != nil {
		return fmt.Errorf("apply deny-read ACLs: %w", err)
	}
	if len(appliedDenyReadPaths) > 0 {
		setupLogLine(log, fmt.Sprintf("applied %d deny-read ACLs", len(appliedDenyReadPaths)))
	}

	if len(payload.ReadRoots) == 0 {
		setupLogLine(log, "no read roots to grant; skipping read ACL helper")
	} else if exists, err := ReadACLMutexExists(); err == nil && exists {
		setupLogLine(log, "read ACL helper already running; skipping spawn")
	} else {
		if err != nil {
			setupLogLine(log, fmt.Sprintf("read ACL mutex check failed: %v; spawning anyway", err))
		}
		if spawnErr := spawnReadACLHelper(payload); spawnErr != nil {
			if err != nil {
				return setupFailuref(windowssandbox.SetupErrorHelperReadACLHelperSpawnFailed, "spawn read ACL helper failed after mutex error %v: %v", err, spawnErr)
			}
			return setupFailuref(windowssandbox.SetupErrorHelperReadACLHelperSpawnFailed, "spawn read ACL helper failed: %v", spawnErr)
		}
	}

	if payload.RefreshOnly {
		if err := EnsureCodexAppRuntimePathsReadable(sandboxGroupSID, &refreshErrors, log); err != nil {
			return err
		}
	}

	writeMask := uint32(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.FILE_GENERIC_EXECUTE | windows.DELETE | setupFileDeleteChild)
	seenWriteRoots := map[string]bool{}
	for _, root := range payload.WriteRoots {
		key := windowssandbox.CanonicalPathKey(root)
		if key == "" || seenWriteRoots[key] {
			continue
		}
		seenWriteRoots[key] = true
		if _, err := os.Stat(root); err != nil {
			if os.IsNotExist(err) {
				setupLogLine(log, fmt.Sprintf("write root %s missing; skipping", root))
				continue
			}
			return err
		}
		rootCapSID, err := windowssandbox.WorkspaceWriteCapabilitySIDForRootWithCWD(payload.CodexHome, payload.CommandCWD, root)
		if err != nil {
			return err
		}
		needGrant := false
		for _, subject := range []struct {
			label string
			sid   string
		}{
			{label: "sandbox_group", sid: sandboxGroupSID},
			{label: writeCapabilityLabel(payload.CommandCWD, root), sid: rootCapSID},
		} {
			has, err := windowssandbox.PathMaskAllows(windowssandbox.ACLRequest{Path: root, SID: subject.sid, Mask: writeMask}, true)
			if err != nil {
				refreshErrors = append(refreshErrors, fmt.Sprintf("write mask check failed on %s for %s: %v", root, subject.label, err))
				setupLogLine(log, fmt.Sprintf("write mask check failed on %s for %s: %v; continuing", root, subject.label, err))
				needGrant = true
				continue
			}
			if !has {
				needGrant = true
			}
		}
		if needGrant {
			setupLogLine(log, fmt.Sprintf("granting write ACE to %s for sandbox group and capability SID", root))
			for _, sid := range []string{sandboxGroupSID, rootCapSID} {
				if err := windowssandbox.EnsureAllowWriteACEs(windowssandbox.ACLRequest{Path: root, SID: sid}); err != nil {
					refreshErrors = append(refreshErrors, fmt.Sprintf("write ACE failed on %s: %v", root, err))
					setupLogLine(log, fmt.Sprintf("write ACE grant failed on %s: %v", root, err))
				}
			}
		}
	}

	if err := applyDenyWritePaths(payload, log, &refreshErrors); err != nil {
		return err
	}
	if err := lockSandboxBinDir(payload, sandboxGroupSIDBytes, log); err != nil {
		return err
	}
	if payload.RefreshOnly {
		setupLogLine(log, fmt.Sprintf("setup refresh: processed %d write roots (read roots delegated); errors=%v", len(payload.WriteRoots), refreshErrors))
	} else if err := lockPersistentSandboxDirs(payload, sandboxGroupSIDBytes, log); err != nil {
		return err
	}
	if payload.RefreshOnly && len(refreshErrors) > 0 {
		setupLogLine(log, fmt.Sprintf("setup refresh completed with errors: %v", refreshErrors))
		return fmt.Errorf("setup refresh had errors")
	}
	_ = windowssandbox.LogNoteInDir(sbxDir, "setup binary completed")
	return nil
}

type readACLSubjects struct {
	SandboxGroupSID string
	RXSubjectSIDs   []string
}

func applyReadACLs(readRoots []string, subjects readACLSubjects, log io.Writer, refreshErrors *[]string, accessMask uint32, accessLabel string, inheritance uint32) {
	for _, root := range readRoots {
		if _, err := os.Stat(root); err != nil {
			if os.IsNotExist(err) {
				setupLogLine(log, fmt.Sprintf("%s root %s missing; skipping", accessLabel, root))
				continue
			}
			appendSetupRefreshError(refreshErrors, fmt.Sprintf("%s stat failed on %s: %v", accessLabel, root, err))
			setupLogLine(log, fmt.Sprintf("%s stat failed on %s: %v; continuing", accessLabel, root, err))
			continue
		}
		if readMaskAllowsOrLog(root, subjects.RXSubjectSIDs, "", accessMask, accessLabel, refreshErrors, log) {
			continue
		}
		if readMaskAllowsOrLog(root, []string{subjects.SandboxGroupSID}, "sandbox_group", accessMask, accessLabel, refreshErrors, log) {
			continue
		}
		setupLogLine(log, fmt.Sprintf("granting %s ACE to %s for sandbox users", accessLabel, root))
		req := windowssandbox.ACLRequest{Path: root, SID: subjects.SandboxGroupSID, Mask: accessMask}
		if err := windowssandbox.EnsureAllowMaskACEsWithInheritance(req, inheritance); err != nil {
			appendSetupRefreshError(refreshErrors, fmt.Sprintf("grant %s ACE failed on %s for sandbox_group: %v", accessLabel, root, err))
			setupLogLine(log, fmt.Sprintf("grant %s ACE failed on %s for sandbox_group: %v", accessLabel, root, err))
		}
	}
}

func readMaskAllowsOrLog(root string, sids []string, label string, readMask uint32, accessLabel string, refreshErrors *[]string, log io.Writer) bool {
	for _, sid := range sids {
		has, err := windowssandbox.PathMaskAllows(windowssandbox.ACLRequest{Path: root, SID: sid, Mask: readMask}, true)
		if err != nil {
			labelSuffix := ""
			if label != "" {
				labelSuffix = " for " + label
			}
			appendSetupRefreshError(refreshErrors, fmt.Sprintf("%s mask check failed on %s%s: %v", accessLabel, root, labelSuffix, err))
			setupLogLine(log, fmt.Sprintf("%s mask check failed on %s%s: %v; continuing", accessLabel, root, labelSuffix, err))
			continue
		}
		if has {
			return true
		}
	}
	return false
}

func provisionAndHideSandboxUsers(payload *setupPayload, log io.Writer, sbxDir string) error {
	if err := ProvisionSandboxUsers(payload.CodexHome, payload.OfflineUsername, payload.OnlineUsername, log); err != nil {
		if extractSetupFailure(err) != nil {
			return err
		}
		return setupFailuref(windowssandbox.SetupErrorHelperUserProvisionFailed, "provision sandbox users failed: %v", err)
	}
	if err := windowssandbox.HideNewlyCreatedUsers([]string{payload.OfflineUsername, payload.OnlineUsername}, sbxDir); err != nil {
		setupLogLine(log, fmt.Sprintf("hide newly created users failed: %v", err))
	}
	return nil
}

func configureOfflineSandboxNetwork(payload *setupPayload, offlineSID string, log io.Writer) error {
	if err := EnsureOfflineProxyAllowlist(offlineSID, payload.ProxyPorts, payload.AllowLocalBinding, log); err != nil {
		if extractSetupFailure(err) != nil {
			return err
		}
		return setupFailuref(windowssandbox.SetupErrorHelperFirewallRuleCreateOrAddFailed, "ensure offline proxy allowlist failed: %v", err)
	}
	if err := EnsureOfflineOutboundBlock(offlineSID, log); err != nil {
		if extractSetupFailure(err) != nil {
			return err
		}
		return setupFailuref(windowssandbox.SetupErrorHelperFirewallRuleCreateOrAddFailed, "ensure offline outbound block failed: %v", err)
	}
	windowssandbox.InstallWFPFilters(payload.CodexHome, payload.OfflineUsername, func(message string) {
		setupLogLine(log, message)
	})
	return nil
}

func applyDenyWritePaths(payload *setupPayload, log io.Writer, refreshErrors *[]string) error {
	seen := map[string]bool{}
	for _, path := range payload.DenyWritePaths {
		key := windowssandbox.CanonicalPathKey(path)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		if _, err := os.Stat(path); err != nil {
			if !os.IsNotExist(err) {
				return err
			}
			if err := os.MkdirAll(path, 0o700); err != nil {
				return fmt.Errorf("failed to create deny-write path %s: %w", path, err)
			}
		}
		denySIDs, err := workspaceWriteCapSIDsForPath(payload.CodexHome, payload.CommandCWD, payload.WriteRoots, path)
		if err != nil {
			return err
		}
		for _, sid := range denySIDs {
			if err := windowssandbox.AddDenyWriteACE(windowssandbox.ACLRequest{Path: path, SID: sid}); err != nil {
				appendSetupRefreshError(refreshErrors, fmt.Sprintf("deny ACE failed on %s: %v", path, err))
				setupLogLine(log, fmt.Sprintf("deny ACE failed on %s: %v", path, err))
				continue
			}
			setupLogLine(log, fmt.Sprintf("applied deny ACE to protect %s", path))
		}
	}
	return nil
}

func workspaceWriteCapSIDsForPath(codexHome string, commandCWD string, writeRoots []string, path string) ([]string, error) {
	var sidStrings []string
	seen := map[string]bool{}
	for _, root := range writeRoots {
		if !windowssandbox.WorkspaceWriteRootOverlapsPath(root, path) {
			continue
		}
		sid, err := windowssandbox.WorkspaceWriteCapabilitySIDForRootWithCWD(codexHome, commandCWD, root)
		if err != nil {
			return nil, err
		}
		if !seen[sid] {
			seen[sid] = true
			sidStrings = append(sidStrings, sid)
		}
	}
	if len(sidStrings) > 0 {
		return sidStrings, nil
	}
	if len(writeRoots) == 0 {
		sid, err := windowssandbox.WorkspaceWriteCapabilitySIDForRootWithCWD(codexHome, commandCWD, commandCWD)
		if err != nil {
			return nil, err
		}
		return []string{sid}, nil
	}
	for _, root := range writeRoots {
		sid, err := windowssandbox.WorkspaceWriteCapabilitySIDForRootWithCWD(codexHome, commandCWD, root)
		if err != nil {
			return nil, err
		}
		if !seen[sid] {
			seen[sid] = true
			sidStrings = append(sidStrings, sid)
		}
	}
	return sidStrings, nil
}

func writeCapabilityLabel(commandCWD string, root string) string {
	if windowssandbox.IsCommandCWDRoot(commandCWD, root) {
		return "workspace_cap"
	}
	return "root_cap"
}

func spawnReadACLHelper(payload *setupPayload) error {
	readPayload := *payload
	readPayload.Mode = setupModeReadACLsOnly
	readPayload.RefreshOnly = true
	encoded, err := encodeSetupPayload(&readPayload)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate setup helper: %w", err)
	}
	cmd := exec.Command(exe, encoded)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	return cmd.Start()
}

func encodeSetupPayload(payload *setupPayload) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func prepareSetupMarker(codexHome string, realUser string) error {
	if err := os.MkdirAll(windowssandbox.SandboxDir(codexHome), 0o700); err != nil {
		return setupFailuref(windowssandbox.SetupErrorHelperSandboxDirCreateFailed, "failed to create sandbox dir %s: %v", windowssandbox.SandboxDir(codexHome), err)
	}
	if err := os.Remove(windowssandbox.SetupMarkerPath(codexHome)); err != nil && !os.IsNotExist(err) {
		return setupFailuref(windowssandbox.SetupErrorHelperSetupMarkerWriteFailed, "prepare setup marker failed: %v", err)
	}
	return nil
}

func commitSetupMarker(codexHome string, offlineUsername string, onlineUsername string, proxyPorts []uint16, allowLocalBinding bool) error {
	createdAt := time.Now().UTC().Format(time.RFC3339)
	return windowssandbox.WriteSetupMarker(codexHome, &windowssandbox.SetupMarker{
		Version:           windowssandbox.SetupVersion,
		OfflineUsername:   offlineUsername,
		OnlineUsername:    onlineUsername,
		CreatedAt:         &createdAt,
		ProxyPorts:        append([]uint16(nil), proxyPorts...),
		AllowLocalBinding: allowLocalBinding,
	})
}

func lockSandboxBinDir(payload *setupPayload, sandboxGroupSID []byte, log io.Writer) error {
	err := lockSandboxDir(
		windowssandbox.SandboxBinDir(payload.CodexHome),
		payload.RealUser,
		sandboxGroupSID,
		windows.SET_ACCESS,
		uint32(windows.FILE_GENERIC_READ|windows.FILE_GENERIC_EXECUTE),
		uint32(windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.FILE_GENERIC_EXECUTE|windows.DELETE),
	)
	if err != nil {
		return setupFailuref(windowssandbox.SetupErrorHelperSandboxLockFailed, "lock sandbox bin dir %s failed: %v", windowssandbox.SandboxBinDir(payload.CodexHome), err)
	}
	return nil
}

func lockPersistentSandboxDirs(payload *setupPayload, sandboxGroupSID []byte, log io.Writer) error {
	sandboxDir := windowssandbox.SandboxDir(payload.CodexHome)
	err := lockSandboxDir(
		sandboxDir,
		payload.RealUser,
		sandboxGroupSID,
		windows.SET_ACCESS,
		uint32(windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.FILE_GENERIC_EXECUTE|windows.DELETE),
		uint32(windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.FILE_GENERIC_EXECUTE),
	)
	if err != nil {
		return setupFailuref(windowssandbox.SetupErrorHelperSandboxLockFailed, "lock sandbox dir %s failed: %v", sandboxDir, err)
	}
	secretsDir := windowssandbox.SandboxSecretsDir(payload.CodexHome)
	err = lockSandboxDir(
		secretsDir,
		payload.RealUser,
		sandboxGroupSID,
		windows.DENY_ACCESS,
		uint32(windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.FILE_GENERIC_EXECUTE|windows.DELETE),
		uint32(windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.FILE_GENERIC_EXECUTE),
	)
	if err != nil {
		return setupFailuref(windowssandbox.SetupErrorHelperSandboxLockFailed, "lock sandbox secrets dir %s failed: %v", secretsDir, err)
	}
	legacyUsers := filepath.Join(sandboxDir, "sandbox_users.json")
	if err := os.Remove(legacyUsers); err != nil && !os.IsNotExist(err) {
		setupLogLine(log, fmt.Sprintf("remove legacy sandbox users file failed: %v", err))
	}
	return nil
}

func lockSandboxDir(dir string, realUser string, sandboxGroupSID []byte, sandboxGroupAccessMode windows.ACCESS_MODE, sandboxGroupMask uint32, realUserMask uint32) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	systemSID, err := resolveSIDString("SYSTEM")
	if err != nil {
		return err
	}
	adminsSID, err := resolveSIDString("Administrators")
	if err != nil {
		return err
	}
	realSID, err := resolveSIDString(realUser)
	if err != nil {
		return err
	}
	entries := []struct {
		sidString string
		mask      uint32
		mode      windows.ACCESS_MODE
	}{
		{sidString: windowssandbox.StringFromSIDBytes(sandboxGroupSID), mask: sandboxGroupMask, mode: sandboxGroupAccessMode},
		{sidString: systemSID, mask: uint32(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.FILE_GENERIC_EXECUTE | windows.DELETE), mode: windows.SET_ACCESS},
		{sidString: adminsSID, mask: uint32(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.FILE_GENERIC_EXECUTE | windows.DELETE), mode: windows.SET_ACCESS},
		{sidString: realSID, mask: realUserMask, mode: windows.SET_ACCESS},
	}
	explicitEntries := make([]windows.EXPLICIT_ACCESS, 0, len(entries))
	sids := make([]*windows.SID, 0, len(entries))
	for _, entry := range entries {
		sid, err := windows.StringToSid(entry.sidString)
		if err != nil {
			return err
		}
		sids = append(sids, sid)
		explicitEntries = append(explicitEntries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.ACCESS_MASK(entry.mask),
			AccessMode:        entry.mode,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	newACL, err := windows.ACLFromEntries(explicitEntries, nil)
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, newACL, nil); err != nil {
		runtime.KeepAlive(sids)
		runtime.KeepAlive(explicitEntries)
		runtime.KeepAlive(newACL)
		return err
	}
	runtime.KeepAlive(sids)
	runtime.KeepAlive(explicitEntries)
	runtime.KeepAlive(newACL)
	return nil
}

func resolveSIDString(name string) (string, error) {
	sid, err := ResolveSID(name)
	if err != nil {
		return "", err
	}
	return windowssandbox.StringFromSIDBytes(sid), nil
}

func openSetupLog(sbxDir string) (*os.File, error) {
	if err := os.MkdirAll(sbxDir, 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(windowssandbox.CurrentLogFilePathForBaseDir(sbxDir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

func setupLogLine(log io.Writer, line string) {
	if log == nil {
		return
	}
	_, _ = fmt.Fprintf(log, "[%s] %s\n", time.Now().UTC().Format(time.RFC3339), line)
}

func appendSetupRefreshError(refreshErrors *[]string, message string) {
	if refreshErrors != nil {
		*refreshErrors = append(*refreshErrors, message)
	}
}

func setupFailuref(code windowssandbox.SetupErrorCode, format string, args ...interface{}) *windowssandbox.SetupFailure {
	return windowssandbox.NewSetupFailure(code, fmt.Sprintf(format, args...))
}

func extractSetupFailure(err error) *windowssandbox.SetupFailure {
	var failure *windowssandbox.SetupFailure
	if errors.As(err, &failure) {
		return failure
	}
	return nil
}
