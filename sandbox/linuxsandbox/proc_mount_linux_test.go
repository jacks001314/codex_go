//go:build linux

package linuxsandbox

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProcMountFailureDiagnosticsLikeRust(t *testing.T) {
	for _, destination := range []string{"/newroot/proc", "/proc"} {
		for _, reason := range []string{"Invalid argument", "Operation not permitted", "Permission denied"} {
			stderr := "bwrap: Can't mount proc on " + destination + ": " + reason + "\n"
			t.Run(destination+"/"+reason, func(t *testing.T) {
				if !isProcMountFailure(stderr) {
					t.Fatalf("did not recognize %q", stderr)
				}
			})
		}
	}
	for _, stderr := range []string{
		"bwrap: Can't bind mount /dev/null: Operation not permitted",
		"bwrap: Can't mount proc on /proc-other: Operation not permitted",
		"bwrap: Can't mount proc on /proc: Limit exceeded (ENOSPC)",
		"bwrap: Can't mount proc on /newroot/proc: Limit exceeded (ENOSPC)\nbwrap: Permission denied",
		"bwrap: Can't mount proc on /proc:\nPermission denied",
		"bwrap: Can't mount proc on /proc: Permission denied trailing text",
	} {
		t.Run(stderr, func(t *testing.T) {
			if isProcMountFailure(stderr) {
				t.Fatalf("unrelated diagnostic triggered fallback: %q", stderr)
			}
		})
	}
}

func TestProcMountPreflightBoundsStderrLikeRust(t *testing.T) {
	var stderr procPreflightStderr
	input := bytes.Repeat([]byte("x"), procPreflightStderrLimit+1024)
	if n, err := stderr.Write(input); n != len(input) || err != nil {
		t.Fatalf("drain = %d, %v", n, err)
	}
	if len(stderr.bytes) != procPreflightStderrLimit {
		t.Fatalf("stored %d bytes", len(stderr.bytes))
	}
	if n, err := stderr.Write([]byte("discarded")); n != 9 || err != nil {
		t.Fatalf("overflow drain = %d, %v", n, err)
	}
}

func TestProcMountFallbackLaunchPathLikeRust(t *testing.T) {
	for _, test := range []struct {
		name       string
		diagnostic string
		flag       string
		wantLog    string
	}{
		{"old-destination", "bwrap: Can't mount proc on /newroot/proc: Operation not permitted", "", "probe\nmain:no-proc\n"},
		{"new-destination", "bwrap: Can't mount proc on /proc: Permission denied", "", "probe\nmain:no-proc\n"},
		{"unrelated-destination", "bwrap: Can't mount proc on /proc-other: Permission denied", "", "probe\nmain:proc\n"},
		{"unrelated-errno", "bwrap: Can't mount proc on /proc: Limit exceeded (ENOSPC)", "", "probe\nmain:proc\n"},
		{"cross-line", "bwrap: Can't mount proc on /proc: Limit exceeded (ENOSPC)\nbwrap: Permission denied", "", "probe\nmain:proc\n"},
		{"success", "", "", "probe\nmain:proc\n"},
		{"no-proc", "", "--no-proc", "main:no-proc\n"},
		{"inherited-pid", "", "--inherit-pid-namespace", "main:no-proc\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, log, count, err := runProcFallbackHelper(t, test.diagnostic, test.flag, "")
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 23 {
				t.Fatalf("payload exit = %v, output %s", err, output)
			}
			if output != "payload\n" {
				t.Fatalf("preflight leaked output: %q", output)
			}
			if log != test.wantLog {
				t.Fatalf("invocations = %q, want %q", log, test.wantLog)
			}
			if count != "once\n" {
				t.Fatalf("payload executions = %q, want exactly once", count)
			}
		})
	}
}

// The wrapper denies only the preflight's fresh procfs. The actual invocation
// then goes through the installed bwrap and production seccomp helper.
func TestProcMountFallbackNativeEndToEndLikeRust(t *testing.T) {
	real, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("system bwrap is unavailable")
	}
	probe := exec.Command(real, "--unshare-pid", "--ro-bind", "/", "/", "--", "/bin/true")
	if out, err := probe.CombinedOutput(); err != nil {
		t.Skipf("host cannot run bwrap: %v: %s", err, out)
	}
	for _, destination := range []string{"/newroot/proc", "/proc"} {
		t.Run(destination, func(t *testing.T) {
			diagnostic := "bwrap: Can't mount proc on " + destination + ": Operation not permitted"
			output, log, count, err := runProcFallbackHelper(t, diagnostic, "", real)
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 23 {
				t.Fatalf("native payload exit = %v, output %s", err, output)
			}
			if output != "payload\n" || count != "once\n" || log != "probe\nmain:no-proc\n" {
				t.Fatalf("native fallback = output %q, count %q, log %q", output, count, log)
			}
			t.Logf("real bwrap: %s denial -> no-proc fallback; command ran once; exit 23 preserved", destination)
		})
	}
}

func TestProcMountPreflightPlanUsesMinimalPolicyLikeRust(t *testing.T) {
	cmd := &linuxSandboxCommand{
		SandboxPolicyCWD: "/", CommandCWD: "/",
		PermissionProfile: &linuxPermissionProfile{Filesystem: &linuxFilesystemPolicy{
			Kind: "restricted", Entries: []linuxFilesystemEntry{{Access: "read", Special: linuxSpecialPath{Kind: "minimal"}}},
		}},
	}
	plan, err := buildBubblewrapArgs(cmd, []string{resolveProcPreflightTrue()}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.close()
	if len(plan.dataFDs) != 0 {
		t.Fatalf("minimal preflight inherited %d data FDs", len(plan.dataFDs))
	}
	if linuxSandboxArgvWindowIndex(plan.args, []string{"--ro-bind", "/", "/"}) >= 0 {
		t.Fatal("preflight exposed the entire host root")
	}
	if linuxSandboxArgvWindowIndex(plan.args, []string{"--tmpfs", "/", "--dev", "/dev", "--proc", "/proc"}) < 0 {
		t.Fatalf("minimal procfs layout missing: %v", plan.args)
	}
}

func runProcFallbackHelper(t *testing.T, diagnostic, flag, real string) (string, string, string, error) {
	t.Helper()
	cwd, launcherDir := t.TempDir(), t.TempDir()
	logPath := filepath.Join(cwd, "invocations")
	countPath := filepath.Join(cwd, "count")
	path := filepath.Join(launcherDir, "bwrap")
	if err := os.WriteFile(path, []byte(procMountTestLauncher), 0o755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	profile := fmt.Sprintf(`{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"root"}},"access":"read"},{"path":{"type":"path","path":%q},"access":"write"}]},"network":"enabled"}`, cwd)
	payload := []string{"/bin/sh", "-c", "printf 'once\\n' >> \"$CODEX_PROC_TEST_COUNT\"; printf 'payload\\n'; exit 23"}
	args, err := CreateCommandArgsWithSandboxExeAndManagedNetwork(executable, payload, cwd, profile, cwd, false, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if flag != "" {
		args = append(args[:1], append([]string{flag}, args[1:]...)...)
	}
	command := exec.Command(args[0], args[1:]...)
	command.Env = append(os.Environ(),
		"PATH="+launcherDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		linuxSandboxE2EHelperEnv+"=1",
		"CODEX_PROC_TEST_DIAGNOSTIC="+diagnostic,
		"CODEX_PROC_TEST_LOG="+logPath,
		"CODEX_PROC_TEST_COUNT="+countPath,
		"CODEX_PROC_TEST_REAL="+real,
	)
	output, runErr := command.CombinedOutput()
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read invocation log: %v; output %s", err, output)
	}
	count, _ := os.ReadFile(countPath)
	return string(output), string(log), string(count), runErr
}

const procMountTestLauncher = `#!/bin/sh
if [ "$1" = "--help" ]; then
  echo "--as-pid-1 --perms --argv0 --ro-bind-fd"
  exit 0
fi
has_proc=no-proc
after_separator=0
for arg in "$@"; do
  if [ "$after_separator" = 1 ]; then
    if [ "$arg" = "/usr/bin/true" ] || [ "$arg" = "/bin/true" ]; then
      printf 'probe\n' >> "$CODEX_PROC_TEST_LOG"
      if [ -n "$CODEX_PROC_TEST_DIAGNOSTIC" ]; then
        printf '%s\n' "$CODEX_PROC_TEST_DIAGNOSTIC" >&2
        exit 1
      fi
      exit 0
    fi
    break
  fi
  [ "$arg" = "--proc" ] && has_proc=proc
  [ "$arg" = "--" ] && after_separator=1
done
printf 'main:%s\n' "$has_proc" >> "$CODEX_PROC_TEST_LOG"
if [ -n "$CODEX_PROC_TEST_REAL" ]; then
  exec "$CODEX_PROC_TEST_REAL" "$@"
fi
printf 'once\n' >> "$CODEX_PROC_TEST_COUNT"
printf 'payload\n'
exit 23
`
