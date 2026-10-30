package appserver

// managedDaemonNoFileLimit is the soft RLIMIT_NOFILE a managed app-server
// daemon raises itself to (Rust #51470: NOFILE_LIMIT).
const managedDaemonNoFileLimit uint64 = 4096

// managedDaemonNoFileTarget resolves the soft limit to request from the
// inherited values: 4096 capped by the hard limit, never lowering an already
// higher soft limit.
func managedDaemonNoFileTarget(current, maximum uint64) uint64 {
	capped := maximum
	if capped > managedDaemonNoFileLimit {
		capped = managedDaemonNoFileLimit
	}
	if current > capped {
		return current
	}
	return capped
}
