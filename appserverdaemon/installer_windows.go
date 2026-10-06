//go:build windows

package appserverdaemon

import "context"

// runInstallerProcess runs the fetched installer script with the guard
// environment (Rust run_installer_script's Windows branch): PowerShell reads the
// script from stdin and the whole invocation lives in a kill-on-close job, so an
// installer descendant cannot outlive the updater.
func runInstallerProcess(ctx context.Context, script []byte, env map[string]string) error {
	return runWindowsUpdateInstallerWithInput(ctx, "powershell.exe", []string{
		"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		// Force UTF-8 output so non-ASCII installer diagnostics survive the
		// captured stderr tail (Rust #50499).
		"-Command", "[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false); try { Invoke-Expression ([Console]::In.ReadToEnd()) } catch { Write-Error $_; exit 1 }",
	}, script, env)
}
