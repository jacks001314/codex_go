//go:build darwin

package tea

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	codextui "codex_go/tui"
	"golang.org/x/sys/unix"
)

// Exercise the production Program, key decoder, SIGTSTP/SIGCONT, raw-mode
// restoration and TIOCGPGRP against a real controlling PTY and a shell-like
// supervisor in a separate session. The supervisor keeps the job non-orphaned.
func TestDarwinJobControlPTYBackgroundContinue(t *testing.T) {
	if os.Getenv("CODEX_DARWIN_JOB_CONTROL_PTY_HELPER") == "1" {
		runDarwinJobControlPTYHelper(t)
		return
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required for the native PTY supervisor")
	}
	for _, mode := range []string{"inline", "alternate"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, python, "-c", darwinJobControlPTYSupervisor, os.Args[0])
			cmd.Env = append(os.Environ(), "CODEX_DARWIN_JOB_CONTROL_PTY_MODE="+mode)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("native PTY job control: %v\n%s", err, output)
			}
			t.Log(strings.TrimSpace(string(output)))
		})
	}
}

func runDarwinJobControlPTYHelper(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	program, model := newProgramWithModel(ctx, codextui.NewState(nil), Options{Width: 60, Height: 12, NoAltScreen: os.Getenv("CODEX_DARWIN_JOB_CONTROL_PTY_MODE") == "inline"}, os.Stdin, os.Stdin)
	model.setComposerPrompt("draft survives suspend")
	responseRendered := make(chan os.Signal, 1)
	signal.Notify(responseRendered, syscall.SIGUSR1)
	defer signal.Stop(responseRendered)
	go func() {
		// Wait for the program to enter raw mode before typing Ctrl-Z.
		for ctx.Err() == nil {
			state, err := unix.IoctlGetTermios(0, unix.TIOCGETA)
			if err == nil && state.Lflag&unix.ICANON == 0 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		fmt.Println("ready")
		// Wait for release followed by restoration; Quit is sent only after
		// the public RestoreTerminal has returned input to raw mode.
		released := false
		for ctx.Err() == nil {
			state, err := unix.IoctlGetTermios(0, unix.TIOCGETA)
			if err == nil {
				if state.Lflag&unix.ICANON != 0 {
					released = true
				}
				if released && state.Lflag&unix.ICANON == 0 {
					// The supervisor signals only after the terminal has painted
					// a marker typed immediately after the CPR response.
					select {
					case <-responseRendered:
						program.Quit()
					case <-ctx.Done():
					}
					return
				}
			}
			time.Sleep(time.Millisecond)
		}
	}()
	if _, err := program.Run(); err != nil {
		t.Fatalf("PTY program: %v", err)
	}
	if model.composerText() != "draft survives suspend!" {
		t.Fatalf("CPR response entered the composer or its following marker was lost: %q", model.composerText())
	}
	if model.notice != "" {
		t.Fatalf("suspend notice: %s", model.notice)
	}
	fmt.Println("resumed")
}

const darwinJobControlPTYSupervisor = `
import fcntl, os, select, signal, struct, subprocess, sys, termios, time
master, slave = os.openpty()
os.setsid()
fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
signal.signal(signal.SIGTTOU, signal.SIG_IGN)
signal.signal(signal.SIGHUP, signal.SIG_IGN)
env = dict(os.environ, CODEX_DARWIN_JOB_CONTROL_PTY_HELPER="1", TERM="xterm-256color")
child = subprocess.Popen([sys.argv[1], "-test.run=^TestDarwinJobControlPTYBackgroundContinue$", "-test.count=1"],
                         stdin=slave, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                         env=env, preexec_fn=os.setpgrp)
def foreground(pgid):
    fcntl.ioctl(slave, termios.TIOCSPGRP, struct.pack("i", pgid))
def wait_until(predicate, description):
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        if predicate(): return
        time.sleep(.005)
    raise AssertionError("timeout: " + description)
try:
    foreground(child.pid)
    assert select.select([child.stdout], [], [], 5)[0], "no ready marker"
    line = child.stdout.readline()
    assert line.strip() == b"ready", line
    os.write(master, b"\x1a")
    stopped = [False]
    def is_stopped():
        pid, status = os.waitpid(child.pid, os.WNOHANG | os.WUNTRACED)
        if pid:
            assert os.WIFSTOPPED(status), ("job exited instead of suspending", status)
            assert os.WSTOPSIG(status) == signal.SIGTSTP, status
            stopped[0] = True
        return stopped[0]
    wait_until(is_stopped, "SIGTSTP")
    while select.select([master], [], [], .02)[0]:
        os.read(master, 65536)
    foreground(os.getpgrp())
    time.sleep(1.2)
    os.killpg(child.pid, signal.SIGCONT)
    time.sleep(.15)
    assert termios.tcgetattr(slave)[3] & termios.ICANON, "background SIGCONT restored raw mode"
    assert child.poll() is None, "job exited while in background"
    background_output = bytearray()
    while select.select([master], [], [], .02)[0]:
        background_output.extend(os.read(master, 65536))
    assert (bytes([27]) + b"[6n") not in background_output, ("background SIGCONT emitted DSR", bytes(background_output))
    foreground(child.pid)
    resumed_output = bytearray()
    def received(pattern):
        if select.select([master], [], [], .005)[0]:
            resumed_output.extend(os.read(master, 65536))
        return pattern in resumed_output
    wait_until(lambda: received(bytes([27]) + b"[6n"), "resume cursor DSR")
    # A real terminal replies on the same input stream. The following marker
    # proves the response was consumed before the helper checks its composer.
    os.write(master, bytes([27]) + b"[I" + bytes([27]) + b"[7;11R!")
    wait_until(lambda: received(b"!"), "composer marker after CPR")
    os.kill(child.pid, signal.SIGUSR1)
    output = child.communicate(timeout=5)[0]
    assert child.returncode == 0, output.decode(errors="replace")
    assert b"resumed" in output, output
    print("native PTY: Ctrl-Z -> SIGTSTP; background SIGCONT kept canonical mode and emitted no DSR; foreground restored raw mode; draft preserved; resume DSR observed; focus/CPR consumed without entering composer")
finally:
    try: os.killpg(child.pid, signal.SIGKILL)
    except ProcessLookupError: pass
    child.wait()
    os.close(master)
    os.close(slave)
`

func TestDarwinJobControlNoTTYNative(t *testing.T) {
	if os.Getenv("CODEX_DARWIN_JOB_CONTROL_NO_TTY_HELPER") == "1" {
		session, err := openJobControlSession()
		if session != nil {
			session.close()
			t.Fatal("opened a controlling terminal in a detached session")
		}
		if err == nil {
			t.Fatal("missing controlling terminal was accepted")
		}
		fmt.Println(err)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDarwinJobControlNoTTYNative$", "-test.count=1")
	cmd.Env = append(os.Environ(), "CODEX_DARWIN_JOB_CONTROL_NO_TTY_HELPER=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("detached session: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}

func TestDarwinJobControlIgnoredSignalFailsSafely(t *testing.T) {
	if os.Getenv("CODEX_DARWIN_JOB_CONTROL_ORPHAN_HELPER") == "1" {
		// A session leader's process group is orphaned; the kernel discards
		// job-control stop signals. This must return instead of hanging.
		session := &unixJobControlSession{pgid: unix.Getpgrp()}
		if err := session.suspend(context.Background()); err == nil {
			t.Fatal("ignored suspend signal reported success")
		} else {
			fmt.Println(err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDarwinJobControlIgnoredSignalFailsSafely$", "-test.count=1")
	cmd.Env = append(os.Environ(), "CODEX_DARWIN_JOB_CONTROL_ORPHAN_HELPER=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ignored suspend signal: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}
