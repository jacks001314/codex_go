package config

// Rust #49160 (Support projectless TUI sessions with workspace defaults)
// promoted `discover_project_root` in codex-rs/config/src/loader/mod.rs:1505 to
// `pub` so the TUI can ask whether a folder has a project root *without* the cwd
// fallback that `find_project_root` applies (Rust's `find_project_root` keeps
// the fallback: `discover_project_root(..).unwrap_or_else(|| cwd.clone())`).

// DefaultProjectRootMarkers returns the markers used when
// `project_root_markers` is not configured. Go already treats these VCS
// metadata directories as project roots inside
// projectRootMarkerExistsWithMarkers; the helper centralizes that default for
// callers that must probe discovery twice (configured markers, then defaults),
// which is what Rust's tui/src/config_update.rs does with
// `default_project_root_markers()`.
func DefaultProjectRootMarkers() []string {
	return []string{".git", ".hg", ".svn"}
}

// DiscoverProjectRoot ports Rust's `discover_project_root`
// (codex-rs/config/src/loader/mod.rs:1505): walk from cwd to the filesystem
// root and return the nearest ancestor carrying one of the markers, preserving
// absence instead of falling back to cwd. An empty marker list means "no
// discovery at all" - Rust returns `Ok(None)` before walking ancestors, and the
// cwd fallback stays the caller's job (activeProjectRootWithMarkers, Rust's
// `find_project_root`).
func DiscoverProjectRoot(cwd string, markers []string) (string, bool) {
	if len(markers) == 0 {
		return "", false
	}
	for _, dir := range projectAncestorDirs(cwd) {
		if projectRootMarkerExistsWithMarkers(dir, markers) {
			return dir, true
		}
	}
	return "", false
}

// DiscoveredProjectlessFolder ports the projectless probe #49160 added to the
// TUI (codex-rs/tui/src/config_update.rs):
//
//	projectless = discover_project_root(cwd, configured_markers).is_none()
//	    && discover_project_root(cwd, default_project_root_markers()).is_none();
//
// A folder with no project-root marker under either marker set is projectless.
// The second probe keeps a gitfile checkout a project even when configured
// markers exclude Git, which is the upstream regression covered by
// `remote_project_trust_guards_thread_start_and_preserves_repository_decision`.
func DiscoveredProjectlessFolder(cwd string, markers []string) bool {
	if _, found := DiscoverProjectRoot(cwd, markers); found {
		return false
	}
	_, found := DiscoverProjectRoot(cwd, DefaultProjectRootMarkers())
	return !found
}
