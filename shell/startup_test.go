package shell

import "testing"

// TestShellStartupScriptMatchesRustLikeRust pins Rust #45749's exported
// shell_startup_script: the Bash and Zsh interactive seeds keep their exact
// bytes, and every other shell type has no shared startup snippet.
func TestShellStartupScriptMatchesRustLikeRust(t *testing.T) {
	const wantZsh = "if [[ -n \"${ZDOTDIR-}\" ]]; then\n" +
		"  rc=\"$ZDOTDIR/.zshrc\"\n" +
		"elif [[ -n \"${HOME-}\" ]]; then\n" +
		"  rc=\"$HOME/.zshrc\"\n" +
		"else\n" +
		"  rc=\n" +
		"fi\n" +
		"[[ -r \"$rc\" ]] && . \"$rc\"\n"
	const wantBash = "if [ -z \"${BASH_ENV-}\" ] && [ -n \"${HOME-}\" ] && [ -r \"$HOME/.bashrc\" ]; then\n" +
		"  . \"$HOME/.bashrc\"\n" +
		"fi\n"
	if got := ShellStartupScript(ShellZsh); got != wantZsh {
		t.Fatalf("ShellStartupScript(zsh) = %q, want %q", got, wantZsh)
	}
	if got := ShellStartupScript(ShellBash); got != wantBash {
		t.Fatalf("ShellStartupScript(bash) = %q, want %q", got, wantBash)
	}
	for _, shellType := range []ShellType{ShellSh, ShellPowerShell, ShellCmd} {
		if got := ShellStartupScript(shellType); got != "" {
			t.Fatalf("ShellStartupScript(%q) = %q, want empty", shellType, got)
		}
	}
}

// TestSnapshotCaptureUsesShellStartupScriptLikeRust checks that interactive
// snapshot capture seeds the session with the shared startup script while a
// non-interactive capture records state without it (Rust keeps POSIX sh's ENV
// handling inside the capture script).
func TestSnapshotCaptureUsesShellStartupScriptLikeRust(t *testing.T) {
	for _, shellType := range []ShellType{ShellZsh, ShellBash} {
		script, ok := SnapshotCaptureScript(shellType, SnapshotCaptureOptions{Startup: SnapshotStartupInteractive})
		if !ok {
			t.Fatalf("SnapshotCaptureScript(%q) = not supported", shellType)
		}
		if startup := ShellStartupScript(shellType); startup == "" || len(script) <= len(startup) || script[:len(startup)] != startup {
			t.Fatalf("interactive %s capture does not start with the shared startup script: %q", shellType, script)
		}
		plain, ok := SnapshotCaptureScript(shellType, SnapshotCaptureOptions{Startup: SnapshotStartupNonInteractive})
		if !ok {
			t.Fatalf("SnapshotCaptureScript(%q) = not supported", shellType)
		}
		if startup := ShellStartupScript(shellType); len(plain) >= len(startup) && plain[:len(startup)] == startup {
			t.Fatalf("non-interactive %s capture unexpectedly contains the startup script", shellType)
		}
	}
}
