//go:build !windows

package tea

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"

	"codex_go/internal/fakeappserver"
	"codex_go/utils"
)

// These tests drive the real TUI binary on a real pty against the fake app
// server (scripts/fakeappserver). They exist because "the unit test passed" is
// not evidence that the terminal surface behaves: they assert the order the
// user sees (progress line first, result later) on the bytes the process
// actually wrote, and the request count the server actually received.

// lockedBuffer is a goroutine-safe capture of the pty stream / server log.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// ptySession is one real TUI process attached to a pty.
type ptySession struct {
	t    *testing.T
	cmd  *exec.Cmd
	ptmx *os.File
	out  *lockedBuffer
	srv  *fakeappserver.Server
	logs *lockedBuffer
}

func (s *ptySession) screen() string {
	return utils.StripANSI(s.out.String())
}

func (s *ptySession) send(text string) {
	s.t.Helper()
	if _, err := s.ptmx.Write([]byte(text)); err != nil {
		s.t.Fatalf("write %q to the pty: %v", text, err)
	}
}

// waitFor polls the terminal until the rendered text contains substring and
// returns the moment it first did. The screen is re-read on every 25ms tick, so
// the returned time is accurate to a frame.
func (s *ptySession) waitFor(sub string, timeout time.Duration) (time.Time, bool) {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if strings.Contains(s.screen(), sub) {
			return time.Now(), true
		}
		if time.Now().After(deadline) {
			return time.Time{}, false
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (s *ptySession) mustWaitFor(sub string, timeout time.Duration) time.Time {
	s.t.Helper()
	at, ok := s.waitFor(sub, timeout)
	if !ok {
		s.t.Fatalf("timed out after %s waiting for %q\nserver log:\n%s\nscreen:\n%s",
			timeout, sub, s.logs.String(), s.screen())
	}
	return at
}

func (s *ptySession) close() {
	if s.ptmx != nil {
		_ = s.ptmx.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
	}
	if s.srv != nil {
		_ = s.srv.Close()
	}
}

// codexBinary builds ./cmd/codex once per test run.
var (
	codexBinOnce sync.Once
	codexBinPath string
	codexBinErr  error
)

func buildCodexBinary(t *testing.T) string {
	t.Helper()
	codexBinOnce.Do(func() {
		goBin, err := exec.LookPath("go")
		if err != nil {
			codexBinErr = err
			return
		}
		root, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			codexBinErr = err
			return
		}
		dir, err := os.MkdirTemp("", "codex-pty-bin")
		if err != nil {
			codexBinErr = err
			return
		}
		out := filepath.Join(dir, "codex")
		build := exec.Command(goBin, "build", "-o", out, "./cmd/codex")
		build.Dir = root
		combined, err := build.CombinedOutput()
		if err != nil {
			codexBinErr = err
			codexBinPath = string(combined)
			return
		}
		codexBinPath = out
	})
	if codexBinErr != nil {
		blob := codexBinPath
		codexBinPath = ""
		// A missing toolchain or module download is an environment problem, not
		// a regression: skip. Anything else is a real build failure.
		lower := strings.ToLower(blob)
		if strings.Contains(lower, "no required module provides") || strings.Contains(lower, "dial tcp") ||
			strings.Contains(lower, "permission denied") || strings.Contains(lower, "lookpath") {
			t.Skipf("cannot build ./cmd/codex here: %v\n%s", codexBinErr, blob)
		}
		t.Fatalf("cannot build ./cmd/codex: %v\n%s", codexBinErr, blob)
	}
	return codexBinPath
}

// startFakeTUI runs `codex --remote unix://<fake>` on an 80x24 pty.
func startFakeTUI(t *testing.T, opts fakeappserver.Options) *ptySession {
	t.Helper()
	if testing.Short() {
		t.Skip("real-terminal test skipped in -short mode")
	}
	bin := buildCodexBinary(t)
	logs := &lockedBuffer{}
	srv, err := fakeappserver.New(fakeappserver.Options{
		ForkDelay: opts.ForkDelay,
		ForkFail:  opts.ForkFail,
		ForkClose: opts.ForkClose,
		ReadOnly:  opts.ReadOnly,
		LogWriter: logs,
	})
	if err != nil {
		t.Skipf("cannot start the fake app server (no unix sockets here?): %v", err)
	}
	cmd := exec.Command(bin, "--remote", "unix://"+srv.SocketPath())
	home := t.TempDir()
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"NO_COLOR=1",
		"HOME="+home,
		"CODEX_HOME="+home,
	)
	cmd.Dir = home
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		_ = srv.Close()
		t.Skipf("cannot allocate a pty: %v", err)
	}
	session := &ptySession{t: t, cmd: cmd, ptmx: ptmx, out: &lockedBuffer{}, srv: srv, logs: logs}
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				_, _ = session.out.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(session.close)
	return session
}

// attachLockedThread resumes the active-writer thread so the TUI shows the
// "This conversation is open in another app" card.
func (s *ptySession) attachLockedThread() {
	s.t.Helper()
	s.mustWaitFor("›", 30*time.Second)
	s.send("/resume")
	time.Sleep(300 * time.Millisecond)
	s.send("\r")
	s.mustWaitFor("demo thread for linux harness", 15*time.Second)
	s.send("\r")
	s.mustWaitFor("open in another app", 20*time.Second)
}

// TestRealTerminalLockedForkPaintsProgressBeforeAnsweringLikeRust is the
// end-to-end check for Rust #47185 on Linux: pressing f shows the progress line
// immediately and the fork result arrives only after the backend answers.
func TestRealTerminalLockedForkPaintsProgressBeforeAnsweringLikeRust(t *testing.T) {
	session := startFakeTUI(t, fakeappserver.Options{
		ReadOnly:  true,
		ForkDelay: 1200 * time.Millisecond,
	})
	session.attachLockedThread()
	session.send("f")
	painted := session.mustWaitFor("Forking conversation", 5*time.Second)
	created := session.mustWaitFor("Fork created", 15*time.Second)
	if elapsed := created.Sub(painted); elapsed < 700*time.Millisecond {
		t.Fatalf("result appeared %s after the progress line; the fork did not run off the update loop", elapsed)
	}
	if got := session.srv.ForkCount(); got != 1 {
		t.Fatalf("server saw %d thread/fork requests, want 1", got)
	}
}

// TestRealTerminalSlashForkPaintsProgressLikeRust covers the unlocked `/fork`
// path: the footer notice appears first and the session switches afterwards.
func TestRealTerminalSlashForkPaintsProgressLikeRust(t *testing.T) {
	session := startFakeTUI(t, fakeappserver.Options{
		ReadOnly:  false,
		ForkDelay: 1200 * time.Millisecond,
	})
	session.mustWaitFor("›", 30*time.Second)
	session.send("/resume")
	time.Sleep(300 * time.Millisecond)
	session.send("\r")
	session.mustWaitFor("demo thread for linux harness", 15*time.Second)
	session.send("\r")
	// A normal resume attaches the conversation; the locked card must not show.
	if _, ok := session.waitFor("open in another app", 1500*time.Millisecond); ok {
		t.Fatalf("resume attached a locked view with -readonly=false\n%s", session.screen())
	}
	session.send("/fork")
	time.Sleep(300 * time.Millisecond)
	session.send("\r")
	painted := session.mustWaitFor("Forking conversation", 5*time.Second)
	done := session.mustWaitFor("Forked session", 15*time.Second)
	if elapsed := done.Sub(painted); elapsed < 700*time.Millisecond {
		t.Fatalf("result appeared %s after the progress line; the fork blocked the UI", elapsed)
	}
	if got := session.srv.ForkCount(); got != 1 {
		t.Fatalf("server saw %d thread/fork requests, want 1", got)
	}
}

// TestRealTerminalForkSwallowsKeysWhilePendingLikeRust: a pending fork owns the
// keyboard, so a `/resume` typed during the window must not start a second
// request, and the fork still completes.
func TestRealTerminalForkSwallowsKeysWhilePendingLikeRust(t *testing.T) {
	session := startFakeTUI(t, fakeappserver.Options{
		ReadOnly:  true,
		ForkDelay: 1200 * time.Millisecond,
	})
	session.attachLockedThread()
	resumesBefore := strings.Count(session.logs.String(), `method="thread/resume"`)
	session.send("f")
	session.mustWaitFor("Forking conversation", 5*time.Second)
	// A repeat shortcut plus a slash command: both must be dropped by the
	// pending fork instead of issuing a second request.
	session.send("ff")
	session.send("/resume\r")
	session.mustWaitFor("Fork created", 15*time.Second)
	if got := strings.Count(session.logs.String(), `method="thread/resume"`); got != resumesBefore {
		t.Fatalf("a keystroke during the fork started another resume (before=%d after=%d)\n%s",
			resumesBefore, got, session.logs.String())
	}
	if got := session.srv.ForkCount(); got != 1 {
		t.Fatalf("server saw %d thread/fork requests, want 1", got)
	}
}

// TestRealTerminalQuitDuringForkLikeRust: quitting mid-fork must end the
// process promptly and without a panic; the detached request may still finish
// on the server (the client simply never sees it).
func TestRealTerminalQuitDuringForkLikeRust(t *testing.T) {
	session := startFakeTUI(t, fakeappserver.Options{
		ReadOnly:  true,
		ForkDelay: 1500 * time.Millisecond,
	})
	session.attachLockedThread()
	session.send("f")
	session.mustWaitFor("Forking conversation", 5*time.Second)
	if _, err := session.ptmx.Write([]byte{0x03}); err != nil {
		t.Fatalf("send ctrl+c: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- session.cmd.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() == 0 {
				t.Fatalf("TUI exited with %v", err)
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("TUI did not exit within 10s of ctrl+c during a fork\nscreen:\n%s", session.screen())
	}
	if strings.Contains(session.screen(), "panic:") {
		t.Fatalf("TUI panicked while quitting during a fork:\n%s", session.screen())
	}
	if session.srv.ForkCount() != 1 {
		t.Fatalf("the in-flight request never reached the server (%d)", session.srv.ForkCount())
	}
}

// TestRealTerminalForkSurvivesAppServerDisconnectLikeRust: when the app server
// drops the connection mid-request the TUI reports the failure and keeps the
// locked view instead of hanging or switching to a thread that does not exist.
func TestRealTerminalForkSurvivesAppServerDisconnectLikeRust(t *testing.T) {
	session := startFakeTUI(t, fakeappserver.Options{
		ReadOnly:  true,
		ForkDelay: 300 * time.Millisecond,
		ForkClose: true,
	})
	session.attachLockedThread()
	session.send("f")
	session.mustWaitFor("Forking conversation", 5*time.Second)
	session.mustWaitFor("open in another app", 15*time.Second)
	if strings.Contains(session.screen(), "Fork created") {
		t.Fatalf("a dropped connection reported a successful fork:\n%s", session.screen())
	}
	if session.srv.ForkCount() != 1 {
		t.Fatalf("server saw %d thread/fork requests, want 1", session.srv.ForkCount())
	}
}

// TestRealTerminalAgentsOverviewForkLikeRust covers the command-center `f`
// path: the dashboard shows the progress notice, stays open while the request
// is in flight, and hands the new session back when it answers.
func TestRealTerminalAgentsOverviewForkLikeRust(t *testing.T) {
	session := startFakeTUI(t, fakeappserver.Options{
		ReadOnly:  false,
		ForkDelay: 1200 * time.Millisecond,
	})
	// Attach a thread first: a brand-new remote session has no thread id yet, so
	// there is nothing to fork (same as Rust).
	session.mustWaitFor("›", 30*time.Second)
	session.send("/resume")
	time.Sleep(300 * time.Millisecond)
	session.send("\r")
	session.mustWaitFor("demo thread for linux harness", 15*time.Second)
	session.send("\r")
	time.Sleep(1500 * time.Millisecond)
	session.send("/agents")
	time.Sleep(300 * time.Millisecond)
	session.send("\r")
	session.mustWaitFor("demo thread for linux harness", 20*time.Second)
	// Wait for the dashboard to finish its initial listing (the fork action is
	// ignored while it still says "Loading tasks…").
	time.Sleep(2 * time.Second)
	session.send("f")
	painted := session.mustWaitFor("Forking conversation", 5*time.Second)
	created := session.mustWaitFor("Forked session", 20*time.Second)
	if elapsed := created.Sub(painted); elapsed < 700*time.Millisecond {
		t.Fatalf("result appeared %s after the progress notice", elapsed)
	}
	if got := session.srv.ForkCount(); got != 1 {
		t.Fatalf("server saw %d thread/fork requests, want 1", got)
	}
}
