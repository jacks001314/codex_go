//go:build windows

package windowssandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const auditTimeLimit = 2 * time.Second

func ApplyWorldWritableScanAndDeniesForPermissions(cwd string) (*WorldWritableAuditResult, error) {
	return ApplyWorldWritableScanAndDenies(&WorldWritableAuditRequest{CWD: cwd})
}

func ApplyWorldWritableScanAndDenies(req *WorldWritableAuditRequest) (*WorldWritableAuditResult, error) {
	if req == nil || req.CWD == "" {
		return nil, ErrInvalidRequest
	}
	flagged, err := AuditEveryoneWritable(req.CWD, req.Env, req.LogsBaseDir)
	if err != nil {
		return worldWritableAuditResult(flagged, true), err
	}
	if len(flagged) == 0 {
		return worldWritableAuditResult(nil, false), nil
	}
	if err := applyCapabilityDeniesForWorldWritable(req, flagged); err != nil {
		_ = logAuditNote(req.LogsBaseDir, fmt.Sprintf("AUDIT: failed to apply capability deny ACEs: %v", err))
	}
	return worldWritableAuditResult(flagged, false), nil
}

func AuditEveryoneWritable(cwd string, env map[string]string, logsBaseDir string) ([]string, error) {
	start := time.Now()
	seen := map[string]bool{}
	var flagged []string
	checked := 0
	checkWorldWritable := func(path string) bool {
		has, err := pathHasWorldWriteAllow(path)
		if err != nil {
			_ = logAuditNote(logsBaseDir, fmt.Sprintf("AUDIT: treating unreadable ACL as not world-writable: %s (%v)", path, err))
			return false
		}
		return has
	}

	scanImmediateChildren(cwd, start, &checked, seen, &flagged, checkWorldWritable)
	for _, root := range gatherAuditCandidates(cwd, env) {
		if auditExpired(start, checked) {
			break
		}
		checked++
		if checkWorldWritable(root) {
			pushAuditFlagged(seen, &flagged, root)
		}
		scanImmediateChildren(root, start, &checked, seen, &flagged, checkWorldWritable)
	}

	elapsedMS := time.Since(start).Milliseconds()
	if len(flagged) == 0 {
		_ = logAuditNote(logsBaseDir, fmt.Sprintf("AUDIT: world-writable scan OK; checked=%d; duration_ms=%d", checked, elapsedMS))
		return nil, nil
	}
	_ = logAuditNote(logsBaseDir, fmt.Sprintf("AUDIT: world-writable scan FAILED; cwd=%s; checked=%d; duration_ms=%d; flagged=%d", cwd, checked, elapsedMS, len(flagged)))
	return flagged, nil
}

func scanImmediateChildren(root string, start time.Time, checked *int, seen map[string]bool, flagged *[]string, checkWorldWritable func(string) bool) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	limit := len(entries)
	if limit > maxAuditItemsPerDir {
		limit = maxAuditItemsPerDir
	}
	for _, entry := range entries[:limit] {
		if auditExpired(start, *checked) {
			return
		}
		info, err := entry.Info()
		if err != nil || info == nil || !info.IsDir() {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepathJoin(root, entry.Name())
		if shouldSkipAuditDir(path) {
			continue
		}
		*checked = *checked + 1
		if checkWorldWritable(path) {
			pushAuditFlagged(seen, flagged, path)
		}
	}
}

func auditExpired(start time.Time, checked int) bool {
	return time.Since(start) > auditTimeLimit || checked > maxAuditCheckedLimit
}

func pushAuditFlagged(seen map[string]bool, flagged *[]string, path string) {
	canonical, err := CanonicalizePath(path)
	if err != nil {
		return
	}
	key := CanonicalPathKey(canonical)
	if seen[key] {
		return
	}
	seen[key] = true
	*flagged = append(*flagged, canonical)
}

func pathHasWorldWriteAllow(path string) (bool, error) {
	world, err := worldSIDBytes()
	if err != nil {
		return false, err
	}
	sd, dacl, err := fileDACL(path)
	if err != nil {
		return false, err
	}
	writeMask := uint32(0x00000002 | 0x00000004 | 0x00000010 | 0x00000100)
	has := daclAllowsAnyMaskForSID(dacl, sidPointerFromBytes(world), writeMask)
	runtime.KeepAlive(world)
	runtime.KeepAlive(sd)
	return has, nil
}

func applyCapabilityDeniesForWorldWritable(req *WorldWritableAuditRequest, flagged []string) error {
	if req == nil || req.CodexHome == "" || req.Permissions == nil || len(flagged) == 0 {
		return nil
	}
	if err := os.MkdirAll(req.CodexHome, 0o700); err != nil {
		return err
	}
	caps, err := LoadOrCreateCapabilitySIDs(req.CodexHome)
	if err != nil {
		return err
	}
	if !req.Permissions.IsEnforceableByWindowsSandbox() {
		return nil
	}

	var activeSIDs []string
	var workspaceRoots []string
	if req.Permissions.UsesWriteCapabilitiesForCWD(req.CWD, req.Env) {
		roots := EffectiveWriteRootsForSetup(req.Permissions, req.CWD, req.Env, req.CodexHome, nil, false)
		workspaceRoots = roots
		for _, root := range roots {
			sid, err := WorkspaceWriteCapabilitySIDForRootWithCWD(req.CodexHome, req.CWD, root)
			if err != nil {
				return err
			}
			activeSIDs = append(activeSIDs, sid)
		}
	} else {
		activeSIDs = []string{caps.Readonly}
	}

	for _, path := range flagged {
		if anyWorkspaceRootContains(workspaceRoots, path) {
			continue
		}
		for _, sid := range activeSIDs {
			if err := AddDenyWriteACE(ACLRequest{Path: path, SID: sid}); err != nil {
				_ = logAuditNote(req.LogsBaseDir, fmt.Sprintf("AUDIT: failed to apply capability deny ACE to %s: %v", path, err))
				continue
			}
			_ = logAuditNote(req.LogsBaseDir, fmt.Sprintf("AUDIT: applied capability deny ACE to %s", path))
		}
	}
	return nil
}

func anyWorkspaceRootContains(roots []string, path string) bool {
	for _, root := range roots {
		if WorkspaceWriteRootContainsPath(root, path) {
			return true
		}
	}
	return false
}

func logAuditNote(logsBaseDir string, note string) error {
	if logsBaseDir == "" {
		return nil
	}
	return LogNoteInDir(logsBaseDir, note)
}

func filepathJoin(elem ...string) string {
	return filepath.Join(elem...)
}
