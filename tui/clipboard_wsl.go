package tui

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Rust parity: codex-rs/tui/src/clipboard_copy/wsl.rs (#61724).
//
// WSL text copies use the Windows clipboard without contacting the display. The
// lightweight clip.exe writer handles single-line printable ASCII and tabs;
// PowerShell preserves everything else. Discovery, input, and exit are bounded,
// and the Windows PowerShell lookup is deferred until a fallback is needed so a
// distro-provided powershell.exe adapter keeps precedence.

const (
	// wslClipboardDeadline bounds one whole copy attempt.
	wslClipboardDeadline = 5 * time.Second
	// wslClipboardDiscoveryBudget bounds executable discovery (Rust's 200 ms).
	wslClipboardDiscoveryBudget = 200 * time.Millisecond
	// wslClipboardClipBudget bounds the clip.exe attempt (Rust's one second)
	// before PowerShell takes over.
	wslClipboardClipBudget = time.Second
)

// WSLClipboardPowerShellArgs is the released PowerShell writer signature.
// Installed Linux adapters may match it exactly, so it must not change.
var WSLClipboardPowerShellArgs = []string{
	"-NoProfile",
	"-Command",
	"[Console]::InputEncoding = [System.Text.Encoding]::UTF8; $ErrorActionPreference = 'Stop'; $text = [Console]::In.ReadToEnd(); Set-Clipboard -Value $text",
}

// WSLClipboardOptions supplies the platform hooks of the WSL writer.
type WSLClipboardOptions struct {
	// SystemPath resolves the trusted child PATH (Rust system_path).
	SystemPath func() (string, bool)
	// WindowsExecutable finds a Windows interop helper (Rust system_executable).
	WindowsExecutable func(name string) (string, bool)
	// LinuxExecutable finds a helper installed on the Linux side only (Rust
	// linux_system_executable).
	LinuxExecutable func(name string) (string, bool)
	// Run executes a writer with text on stdin through the native shell.
	Run func(ctx context.Context, executable string, args []string, path string, text string) error
	// Now reports the current time; tests replace it.
	Now func() time.Time
}

type wslClipboardSetup struct {
	path    string
	adapter string
}

type wslPending[T any] struct {
	mu    sync.Mutex
	wait  chan struct{}
	value T
	err   error
}

// await shares one in-flight setup between callers. A timed-out task keeps
// running and its result is reused by the next caller (Rust read_only_setup);
// a completed task clears the cache so the next call re-resolves.
func (p *wslPending[T]) await(ctx context.Context, setup func() (T, error)) (T, error) {
	var zero T
	p.mu.Lock()
	if p.wait == nil {
		p.wait = make(chan struct{})
		p.value = zero
		p.err = nil
		wait := p.wait
		go func() {
			value, err := setup()
			p.mu.Lock()
			p.value, p.err = value, err
			p.mu.Unlock()
			close(wait)
		}()
	}
	wait := p.wait
	p.mu.Unlock()
	select {
	case <-wait:
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return zero, ClipboardError("clipboard setup timed out")
		}
		return zero, fmt.Errorf("clipboard setup: %w", ctx.Err())
	}
	p.mu.Lock()
	value, err := p.value, p.err
	p.wait = nil
	p.mu.Unlock()
	return value, err
}
// WSLClipboard writes text to the Windows clipboard from a WSL session.
type WSLClipboard struct {
	options    WSLClipboardOptions
	setup      wslPending[wslClipboardSetup]
	clip       wslPending[string]
	powerShell wslPending[string]
}

// NewWSLClipboard builds a writer from platform hooks.
func NewWSLClipboard(options WSLClipboardOptions) *WSLClipboard {
	if options.Now == nil {
		options.Now = time.Now
	}
	return &WSLClipboard{options: options}
}

// WSLClipInput reports the stdin clip.exe accepts for text: single-line
// printable ASCII plus tabs. clip.exe normalizes newlines and guesses UTF-16 for
// some even-length ASCII, so an even byte count is padded with a trailing NUL
// that does not enter the clipboard (Rust clip_input).
func WSLClipInput(text string) (string, bool) {
	if text == "" {
		return "", false
	}
	for i := 0; i < len(text); i++ {
		b := text[i]
		graphic := b >= 0x21 && b <= 0x7e
		if !graphic && b != ' ' && b != '\t' {
			return "", false
		}
	}
	if len(text)%2 == 0 {
		return text + "\x00", true
	}
	return text, true
}

// Copy writes text through clip.exe and falls back to PowerShell.
func (c *WSLClipboard) Copy(text string) error {
	if c == nil {
		return ClipboardError("WSL clipboard fallback unavailable on this platform")
	}
	deadline := c.options.Now().Add(wslClipboardDeadline)
	setup, err := c.resolveSetup(deadline)
	if err != nil {
		return err
	}
	if setup.adapter != "" {
		// An installed Linux adapter wins over both Windows writers.
		return c.write(setup.adapter, WSLClipboardPowerShellArgs, setup.path, text, deadline, "powershell.exe")
	}
	var clipError error
	if input, ok := WSLClipInput(text); ok {
		if clip, clipErr := c.resolveClip(deadline); clipErr == nil && clip != "" {
			clipDeadline := c.options.Now().Add(wslClipboardClipBudget)
			if clipDeadline.After(deadline) {
				clipDeadline = deadline
			}
			if err := c.write(clip, nil, setup.path, input, clipDeadline, "clip.exe"); err == nil {
				return nil
			} else {
				clipError = err
			}
		}
	}
	fallback := c.resolvePowerShell(deadline)
	if fallback.err == nil && fallback.path == "" {
		fallback.err = ClipboardError("PowerShell is unavailable in the system PATH")
	}
	if fallback.err == nil {
		fallback.err = c.write(fallback.path, WSLClipboardPowerShellArgs, setup.path, text, deadline, "powershell.exe")
	}
	if fallback.err == nil {
		return nil
	}
	if clipError != nil {
		return ClipboardError(fmt.Sprintf("%v; %v", clipError, fallback.err))
	}
	return fallback.err
}

func (c *WSLClipboard) resolveSetup(deadline time.Time) (wslClipboardSetup, error) {
	remaining := c.remainingBudget(deadline, 0)
	if remaining <= 0 {
		return wslClipboardSetup{}, ClipboardError("clipboard setup timed out")
	}
	ctx, cancel := context.WithTimeout(context.Background(), remaining)
	defer cancel()
	setup, err := c.setup.await(ctx, func() (wslClipboardSetup, error) {
		path, ok := c.options.SystemPath()
		if !ok {
			return wslClipboardSetup{}, ClipboardError("failed to resolve system PATH")
		}
		adapter := ""
		if c.options.LinuxExecutable != nil {
			if found, ok := c.options.LinuxExecutable("powershell.exe"); ok {
				adapter = found
			}
		}
		return wslClipboardSetup{path: path, adapter: adapter}, nil
	})
	if err != nil {
		return wslClipboardSetup{}, err
	}
	return setup, nil
}

func (c *WSLClipboard) resolveClip(deadline time.Time) (string, error) {
	remaining := c.remainingBudget(deadline, wslClipboardDiscoveryBudget)
	if remaining <= 0 {
		return "", ClipboardError("clipboard setup timed out")
	}
	ctx, cancel := context.WithTimeout(context.Background(), remaining)
	defer cancel()
	return c.clip.await(ctx, func() (string, error) {
		if c.options.WindowsExecutable == nil {
			return "", nil
		}
		tool, _ := c.options.WindowsExecutable("clip.exe")
		return tool, nil
	})
}

type wslResolvedWriter struct {
	path string
	err  error
}

func (c *WSLClipboard) resolvePowerShell(deadline time.Time) wslResolvedWriter {
	remaining := c.remainingBudget(deadline, 0)
	if remaining <= 0 {
		return wslResolvedWriter{err: ClipboardError("clipboard setup timed out")}
	}
	ctx, cancel := context.WithTimeout(context.Background(), remaining)
	defer cancel()
	path, err := c.powerShell.await(ctx, func() (string, error) {
		if c.options.WindowsExecutable == nil {
			return "", nil
		}
		tool, _ := c.options.WindowsExecutable("powershell.exe")
		return tool, nil
	})
	return wslResolvedWriter{path: path, err: err}
}

func (c *WSLClipboard) write(executable string, args []string, path string, text string, deadline time.Time, name string) error {
	remaining := c.remainingBudget(deadline, 0)
	if remaining <= 0 {
		return ClipboardError("Windows clipboard writer timed out")
	}
	ctx, cancel := context.WithTimeout(context.Background(), remaining)
	defer cancel()
	if c.options.Run == nil {
		return ClipboardError("Windows clipboard writer unavailable on this platform")
	}
	if err := c.options.Run(ctx, executable, args, path, text); err != nil {
		if ctx.Err() != nil {
			return ClipboardError("Windows clipboard writer timed out")
		}
		return ClipboardError(name + ": " + err.Error())
	}
	return nil
}

// remainingBudget reports how long a phase may run: the overall copy deadline
// minus now, capped by the phase budget when one applies.
func (c *WSLClipboard) remainingBudget(deadline time.Time, budget time.Duration) time.Duration {
	remaining := deadline.Sub(c.options.Now())
	if budget > 0 && budget < remaining {
		return budget
	}
	return remaining
}
