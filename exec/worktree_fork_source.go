package exec

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"codex_go/cli"
	"codex_go/config"
	"codex_go/rollout"
	"codex_go/session"
)

// execWorktreeUntrustedSourceError is the Rust exec source-policy gate for
// `--worktree` (exec/src/lib.rs, Rust #43069): an explicitly untrusted source
// project is never upgraded by allocating a checkout from it.
var execWorktreeUntrustedSourceError = errors.New("--worktree requires a source that is not explicitly untrusted")

// execForkSourceCWD resolves the working directory a `codex exec fork
// --worktree` checkout must be created from. Rust #43069 routes the fork
// through the app-server before the worktree is allocated, then uses the
// source thread's latest turn cwd so the new checkout branches from where the
// conversation actually ran, not from the invocation directory.
func (r *Runner) execForkSourceCWD(req *Request) (string, error) {
	if r == nil || req == nil || req.Exec.Subcommand != "fork" {
		return "", nil
	}
	target := strings.TrimSpace(req.Exec.Fork.SessionID)
	if target == "" {
		return "", nil
	}
	resumeReq := *req
	resumeReq.Exec.Subcommand = "resume"
	resumeReq.Exec.Resume = cli.ExecResumeOptions{SessionID: target, All: true}
	source, err := r.resolveExecResumeRecordForMethod(&resumeReq, execThreadForkMethod)
	if err != nil {
		if errors.Is(err, errExecResumeNoTarget) {
			return "", errors.New("Session not found: " + target)
		}
		return "", err
	}
	return r.latestThreadCWD(source), nil
}

// execWorktreeSourceTrusted mirrors the source-config gate Rust builds with
// ignore_project_config before it creates a managed checkout. The saved
// projects table still decides whether the source is explicitly untrusted.
func execWorktreeSourceTrusted(cfg *config.Config, sourceCWD string) error {
	if cfg == nil || strings.TrimSpace(sourceCWD) == "" {
		return execWorktreeUntrustedSourceError
	}
	if level, ok := config.ProjectTrustLevelForTarget(cfg.Values, sourceCWD); ok && strings.EqualFold(strings.TrimSpace(level), "untrusted") {
		return execWorktreeUntrustedSourceError
	}
	return nil
}

// latestThreadCWD mirrors Rust exec's latest_thread_cwd: the newest persisted
// turn_context cwd wins, and the thread record's cwd is the fallback when the
// rollout has no turn context (or cannot be read).
func (r *Runner) latestThreadCWD(record *session.Record) string {
	if r == nil || record == nil {
		return ""
	}
	for _, archived := range []bool{false, true} {
		path, err := rollout.FindThreadPath(r.CodexHome, string(record.ID), archived)
		if err != nil {
			continue
		}
		if cwd := latestRolloutTurnContextCWD(path); cwd != "" {
			return cwd
		}
		break
	}
	return strings.TrimSpace(record.Metadata.CWD)
}

// execRolloutTurnContextLine accepts both the Rust wire shape
// ({"type":"turn_context","payload":{"cwd":...}}) and the legacy Go sibling key
// the rollout reader also honors, so the newest turn context is found in
// rollouts written by either implementation.
type execRolloutTurnContextLine struct {
	Type    string `json:"type"`
	Payload struct {
		CWD string `json:"cwd"`
	} `json:"payload"`
	TurnContext struct {
		CWD string `json:"cwd"`
	} `json:"turn_context"`
}

// latestRolloutTurnContextCWD scans a rollout front-to-back and keeps the last
// turn_context cwd. Rust uses a reverse scanner for the same answer; forward
// streaming keeps memory bounded here while preserving the newest value.
// Unparsable lines are skipped, while a read error abandons the lookup so the
// caller falls back to the thread cwd exactly like Rust's scanner.
func latestRolloutTurnContextCWD(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	reader, err := rollout.OpenRolloutLineReader(path)
	if err != nil {
		return ""
	}
	defer func() { _ = reader.Close() }()
	latest := ""
	for {
		raw, readErr := reader.ReadLine()
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return ""
		}
		raw = []byte(strings.TrimSpace(string(raw)))
		if len(raw) == 0 {
			continue
		}
		var line execRolloutTurnContextLine
		if err := json.Unmarshal(raw, &line); err != nil {
			continue
		}
		if line.Type != "turn_context" {
			continue
		}
		if cwd := strings.TrimSpace(firstNonEmpty(line.Payload.CWD, line.TurnContext.CWD)); cwd != "" {
			latest = cwd
		}
	}
	return latest
}
