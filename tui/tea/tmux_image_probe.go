package tea

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"codex_go/tui/pets"
)

// tmux image probe (Rust #59579/#59580): one `tmux display-message` call tells
// us whether this pane may carry Kitty passthrough images.
const tmuxImageProbeFormat = "#{allow-passthrough}\t#{client_termname}\t#{client_termtype}\t#{client_termfeatures}\t#{pane_id}"

// runTmuxImageProbe is replaceable so tests never shell out to tmux.
var runTmuxImageProbe = func(pane string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	args := []string{"display-message", "-p"}
	if strings.TrimSpace(pane) != "" {
		args = append(args, "-t", strings.TrimSpace(pane))
	}
	args = append(args, tmuxImageProbeFormat)
	out, err := exec.CommandContext(ctx, "tmux", args...).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// probeTmuxImageSupport reads the tmux client fields when this process runs
// inside tmux. A missing tmux or an inconclusive probe reports nil, which keeps
// images disabled in the pane.
func probeTmuxImageSupport(env map[string]string) *pets.TmuxProbe {
	if len(env) == 0 {
		return nil
	}
	if strings.TrimSpace(env["TMUX"]) == "" && strings.TrimSpace(env["TMUX_PANE"]) == "" {
		return nil
	}
	output, err := runTmuxImageProbe(env["TMUX_PANE"], time.Second)
	if err != nil {
		return nil
	}
	probe, ok := pets.ParseTmuxProbe(output)
	if !ok {
		return nil
	}
	return &probe
}
