package windowssandbox

type PortStatus string

const (
	PortStatusScaffold PortStatus = "scaffold"
	PortStatusStub     PortStatus = "stub"
	PortStatusDone     PortStatus = "implemented"
)

type PortModule struct {
	RustPath string
	GoPath   string
	Package  string
	Status   PortStatus
	Notes    string
}

func PortLayout() []PortModule {
	return append([]PortModule(nil), portLayout...)
}

var portLayout = []PortModule{
	{RustPath: "src/lib.rs", GoPath: "sandbox/windowssandbox", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/acl.rs", GoPath: "sandbox/windowssandbox/acl.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/allow.rs", GoPath: "sandbox/windowssandbox/allow.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/audit.rs", GoPath: "sandbox/windowssandbox/audit.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/cap.rs", GoPath: "sandbox/windowssandbox/cap.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/deny_read_acl.rs", GoPath: "sandbox/windowssandbox/deny_read_acl.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/deny_read_resolver.rs", GoPath: "sandbox/windowssandbox/deny_read_resolver.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/deny_read_state.rs", GoPath: "sandbox/windowssandbox/deny_read_state.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/desktop.rs", GoPath: "sandbox/windowssandbox/desktop.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/dpapi.rs", GoPath: "sandbox/windowssandbox/dpapi.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/elevated_impl.rs", GoPath: "sandbox/windowssandbox/elevated_impl.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/env.rs", GoPath: "sandbox/windowssandbox/env.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/helper_materialization.rs", GoPath: "sandbox/windowssandbox/helper_materialization.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/hide_users.rs", GoPath: "sandbox/windowssandbox/hide_users.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/identity.rs", GoPath: "sandbox/windowssandbox/identity.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/logging.rs", GoPath: "sandbox/windowssandbox/logging.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/path_normalization.rs", GoPath: "sandbox/windowssandbox/path_normalization.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/process.rs", GoPath: "sandbox/windowssandbox/process.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/proc_thread_attr.rs", GoPath: "sandbox/windowssandbox/proc_thread_attr.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/resolved_permissions.rs", GoPath: "sandbox/windowssandbox/resolved_permissions.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/sandbox_utils.rs", GoPath: "sandbox/windowssandbox/sandbox_utils.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/setup.rs", GoPath: "sandbox/windowssandbox/setup.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/setup_error.rs", GoPath: "sandbox/windowssandbox/setup_error.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/spawn_prep.rs", GoPath: "sandbox/windowssandbox/spawn_prep.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/ssh_config_dependencies.rs", GoPath: "sandbox/windowssandbox/ssh_config_dependencies.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/stdio_bridge.rs", GoPath: "sandbox/windowssandbox/stdio_bridge.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/token.rs", GoPath: "sandbox/windowssandbox/token.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/wfp.rs", GoPath: "sandbox/windowssandbox/wfp.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/wfp_setup.rs", GoPath: "sandbox/windowssandbox/wfp_setup.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/winutil.rs", GoPath: "sandbox/windowssandbox/winutil.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/workspace_acl.rs", GoPath: "sandbox/windowssandbox/workspace_acl.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/wrapper.rs", GoPath: "sandbox/windowssandbox/wrapper.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/wrapper_tests.rs", GoPath: "sandbox/windowssandbox/wrapper_test.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/stdio_bridge_tests.rs", GoPath: "sandbox/windowssandbox/stdio_bridge_test.go", Package: "windowssandbox", Status: PortStatusDone},
	{RustPath: "src/bin/setup_main/main.rs", GoPath: "sandbox/windowssandbox/bin/setup_main", Package: "setup_main", Status: PortStatusDone},
	{RustPath: "src/bin/setup_main/win.rs", GoPath: "sandbox/windowssandbox/bin/setup_main/win", Package: "win", Status: PortStatusDone},
	{RustPath: "src/bin/setup_main/win/firewall.rs", GoPath: "sandbox/windowssandbox/bin/setup_main/win/firewall.go", Package: "win", Status: PortStatusDone},
	{RustPath: "src/bin/setup_main/win/read_acl_mutex.rs", GoPath: "sandbox/windowssandbox/bin/setup_main/win/read_acl_mutex.go", Package: "win", Status: PortStatusDone},
	{RustPath: "src/bin/setup_main/win/sandbox_users.rs", GoPath: "sandbox/windowssandbox/bin/setup_main/win/sandbox_users.go", Package: "win", Status: PortStatusDone},
	{RustPath: "src/bin/setup_main/win/setup_runtime_bin.rs", GoPath: "sandbox/windowssandbox/bin/setup_main/win/setup_runtime_bin.go", Package: "win", Status: PortStatusDone},
	{RustPath: "src/bin/command_runner/main.rs", GoPath: "sandbox/windowssandbox/bin/command_runner", Package: "command_runner", Status: PortStatusDone},
	{RustPath: "src/bin/command_runner/win.rs", GoPath: "sandbox/windowssandbox/bin/command_runner/win", Package: "win", Status: PortStatusDone},
	{RustPath: "src/bin/command_runner/win/cwd_junction.rs", GoPath: "sandbox/windowssandbox/bin/command_runner/win/cwd_junction.go", Package: "win", Status: PortStatusDone},
	{RustPath: "src/conpty/mod.rs", GoPath: "sandbox/windowssandbox/conpty", Package: "conpty", Status: PortStatusDone, Notes: "Uses golang.org/x/sys/windows ConPTY APIs"},
	{RustPath: "src/elevated/mod.rs", GoPath: "sandbox/windowssandbox/elevated", Package: "elevated", Status: PortStatusDone},
	{RustPath: "src/elevated/ipc_framed.rs", GoPath: "sandbox/windowssandbox/elevated/ipc_framed.go", Package: "elevated", Status: PortStatusDone},
	{RustPath: "src/elevated/runner_client.rs", GoPath: "sandbox/windowssandbox/elevated/runner_client.go", Package: "elevated", Status: PortStatusDone},
	{RustPath: "src/elevated/runner_pipe.rs", GoPath: "sandbox/windowssandbox/elevated/runner_pipe.go", Package: "elevated", Status: PortStatusDone},
	{RustPath: "src/unified_exec/mod.rs", GoPath: "sandbox/windowssandbox/unified_exec", Package: "unified_exec", Status: PortStatusDone},
	{RustPath: "src/unified_exec/tests.rs", GoPath: "sandbox/windowssandbox/unified_exec/tests_test.go", Package: "unified_exec", Status: PortStatusDone},
	{RustPath: "src/unified_exec/backends/mod.rs", GoPath: "sandbox/windowssandbox/unified_exec/backends", Package: "backends", Status: PortStatusDone},
	{RustPath: "src/unified_exec/backends/elevated.rs", GoPath: "sandbox/windowssandbox/unified_exec/backends/elevated.go", Package: "backends", Status: PortStatusDone},
	{RustPath: "src/unified_exec/backends/elevated_tests.rs", GoPath: "sandbox/windowssandbox/unified_exec/backends/elevated_tests_test.go", Package: "backends", Status: PortStatusDone},
	{RustPath: "src/unified_exec/backends/legacy.rs", GoPath: "sandbox/windowssandbox/unified_exec/backends/legacy.go", Package: "backends", Status: PortStatusDone},
	{RustPath: "src/unified_exec/backends/windows_common.rs", GoPath: "sandbox/windowssandbox/unified_exec/backends/windows_common.go", Package: "backends", Status: PortStatusDone},
	{RustPath: "src/wfp/filter_specs.rs", GoPath: "sandbox/windowssandbox/wfp", Package: "wfp", Status: PortStatusDone},
}
