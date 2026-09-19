package windowssandbox

type ProcThreadAttributeList struct {
	impl             any
	handleList       []uintptr
	desktopAppPolicy uint32
}

// ProcThreadAttributeCountForLaunch returns the attribute count for a sandbox
// launch: one attribute for the stdio handle list (regular) or the
// pseudoconsole (ConPTY) plus one when the caller's package identity is
// preserved. Mirrors Rust's `attr_count` / `ProcThreadAttributeList::new(2 +
// u32::from(preserve_app_context))`.
func ProcThreadAttributeCountForLaunch(preserveAppContext bool) uint32 {
	if preserveAppContext {
		return 2
	}
	return 1
}
