package tui

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"unicode/utf8"

	"codex_go/envutil"
)

// TmuxClipboardCommand runs one tmux invocation and returns stdout. A non-nil
// stdin payload is written to the child's standard input.
type TmuxClipboardCommand func(args []string, stdin *string) (string, error)

// TmuxClipboardCopy forwards text through tmux's own clipboard integration for
// the most recently active client attached to the current pane's session
// (Rust #45457, codex-rs/tui/src/clipboard_copy/tmux.rs).
//
// `load-buffer -w -` lets tmux read the text from stdin, keep a matching paste
// buffer, and forward the contents to the outer terminal clipboard without
// relying on DCS passthrough. The parent routing layer enforces the terminal
// payload limit.
func TmuxClipboardCopy(text string) error {
	executable, ok := envutil.TrustedExecutable("tmux")
	if !ok {
		return ClipboardError("tmux is unavailable in the system PATH")
	}
	path, ok := envutil.TrustedSystemPath()
	if !ok {
		return ClipboardError("failed to resolve system PATH")
	}
	pane, paneSet := os.LookupEnv("TMUX_PANE")
	run := func(args []string, stdin *string) (string, error) {
		return runTmuxCommand(executable, path, args, stdin)
	}
	return tmuxClipboardCopyWith(text, pane, paneSet, run)
}

// tmuxClipboardCopyWith is the testable core of TmuxClipboardCopy.
func tmuxClipboardCopyWith(text string, pane string, paneSet bool, run TmuxClipboardCommand) error {
	if !paneSet {
		return ClipboardError("tmux clipboard forwarding is unavailable: no current pane")
	}
	client, err := TmuxClipboardTarget(
		func() (string, error) {
			return run([]string{"show-options", "-gv", "set-clipboard"}, nil)
		},
		func() (string, error) {
			session, err := run([]string{"display-message", "-p", "-t", pane, "#{session_id}"}, nil)
			if err != nil {
				return "", err
			}
			return run(
				[]string{"list-clients", "-t", strings.TrimSpace(session), "-F", "#{client_activity} #{client_name}"},
				nil,
			)
		},
		func(client string) (string, error) {
			return run([]string{"show-messages", "-T", "-t", client}, nil)
		},
	)
	if err != nil {
		return err
	}
	_, err = run([]string{"load-buffer", "-w", "-t", client, "-"}, &text)
	return err
}

// TmuxClipboardTarget resolves the most recently active client in this session
// and verifies it advertises the Ms clipboard capability. Unlike tmux's
// implicit best-client lookup, listing clients for the current pane's session
// cannot select a client from another session.
func TmuxClipboardTarget(
	setClipboard func() (string, error),
	listClients func() (string, error),
	tmuxInfo func(client string) (string, error),
) (string, error) {
	setClipboardOutput, err := setClipboard()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(setClipboardOutput) == "off" {
		return "", ClipboardError("tmux clipboard forwarding is disabled")
	}

	clients, err := listClients()
	if err != nil {
		return "", err
	}
	client, ok := mostActiveTmuxClient(clients)
	if !ok {
		return "", ClipboardError("tmux clipboard forwarding is unavailable: no attached client")
	}

	info, err := tmuxInfo(client)
	if err != nil {
		return "", err
	}
	if !tmuxInfoAdvertisesClipboard(info) {
		return "", ClipboardError("tmux clipboard forwarding is unavailable: missing Ms capability")
	}
	return client, nil
}

// mostActiveTmuxClient picks the last client with the greatest activity, which
// matches Rust's `max_by_key` tie-breaking.
func mostActiveTmuxClient(clients string) (string, bool) {
	best := ""
	var bestActivity uint64
	found := false
	for _, line := range strings.Split(clients, "\n") {
		activity, client, ok := strings.Cut(strings.TrimSuffix(line, "\r"), " ")
		if !ok || client == "" {
			continue
		}
		value, err := strconv.ParseUint(activity, 10, 64)
		if err != nil {
			continue
		}
		if found && value < bestActivity {
			continue
		}
		best, bestActivity, found = client, value, true
	}
	return best, found
}

func tmuxInfoAdvertisesClipboard(info string) bool {
	for _, line := range strings.Split(info, "\n") {
		_, sequence, ok := strings.Cut(strings.TrimSuffix(line, "\r"), "Ms: (string) ")
		if ok && strings.TrimSpace(sequence) != "" {
			return true
		}
	}
	return false
}

func runTmuxCommand(executable string, path string, args []string, stdin *string) (string, error) {
	cmd := exec.Command(executable, args...)
	cmd.Env = tmuxEnvironment(path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	var pipe io.WriteCloser
	if stdin != nil {
		var err error
		pipe, err = cmd.StdinPipe()
		if err != nil {
			return "", ClipboardError("failed to open tmux stdin")
		}
	}
	if err := cmd.Start(); err != nil {
		return "", ClipboardError("failed to spawn tmux: " + err.Error())
	}
	if pipe != nil {
		if _, err := io.WriteString(pipe, *stdin); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return "", ClipboardError("failed to write to tmux: " + err.Error())
		}
		_ = pipe.Close()
	}
	if err := cmd.Wait(); err != nil {
		return "", tmuxCommandError(err, stderr.String())
	}
	if !utf8.ValidString(stdout.String()) {
		return "", ClipboardError("tmux output was not UTF-8")
	}
	return stdout.String(), nil
}

func tmuxCommandError(err error, stderr string) error {
	if exitErr, ok := err.(*exec.ExitError); ok {
		if trimmed := strings.TrimSpace(stderr); trimmed != "" {
			return ClipboardError("tmux failed: " + trimmed)
		}
		return ClipboardError("tmux exited with " + exitErr.Error())
	}
	return ClipboardError("failed to wait for tmux: " + err.Error())
}

// tmuxEnvironment inherits the current environment with PATH replaced by the
// trusted system path, mirroring Rust's `.env("PATH", &path)`.
func tmuxEnvironment(path string) []string {
	environment := os.Environ()
	out := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "PATH") {
			continue
		}
		out = append(out, entry)
	}
	return append(out, "PATH="+path)
}
