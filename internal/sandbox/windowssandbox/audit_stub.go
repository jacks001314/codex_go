//go:build !windows

package windowssandbox

func AuditEveryoneWritable(cwd string, env map[string]string, logsBaseDir string) ([]string, error) {
	return nil, unsupported("audit.audit_everyone_writable")
}

func ApplyWorldWritableScanAndDeniesForPermissions(cwd string) (*WorldWritableAuditResult, error) {
	return nil, unsupported("audit.apply_world_writable_scan_and_denies_for_permissions")
}

func ApplyWorldWritableScanAndDenies(req *WorldWritableAuditRequest) (*WorldWritableAuditResult, error) {
	return nil, unsupported("audit.apply_world_writable_scan_and_denies_for_permissions")
}
