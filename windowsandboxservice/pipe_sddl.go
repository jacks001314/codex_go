package windowsandboxservice

// pipeUserAccess is the interactive-user access mask the provisioning client
// opens the pipe with (Rust ipc::PIPE_USER_ACCESS).
const pipeUserAccess = "0x0012019b"

// PipeSecurityDescriptor returns the DACL applied to the provisioning pipe
// (Rust ipc::pipe_security_descriptor): the sandbox group is denied so a
// sandboxed process cannot talk to the service, while SYSTEM, the built-in
// administrators and interactive users keep access.
func PipeSecurityDescriptor(sandboxSID string) string {
	return "D:P(D;;GA;;;" + sandboxSID + ")(A;;GA;;;SY)(A;;GA;;;BA)(A;;" + pipeUserAccess + ";;;IU)"
}
