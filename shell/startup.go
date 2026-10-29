package shell

// ShellStartupScript mirrors Rust shell-command's shell_startup_script
// (#45749): the interactive configuration seed shared by snapshot capture and
// other initialized shells. The caller owns the shell's launch flags and
// lifecycle; POSIX sh's ENV handling stays in the capture script, and every
// other shell type has no shared startup snippet.
func ShellStartupScript(shellType ShellType) string {
	switch shellType {
	case ShellZsh:
		return shellStartupZsh
	case ShellBash:
		return shellStartupBash
	default:
		return ""
	}
}
