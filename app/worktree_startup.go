package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/worktree"
)

// interactiveWorktreeStartup mirrors Rust tui/src/worktree_startup.rs
// ManagedTuiWorktree: the checkout is allocated before the session starts, the
// thread is bound to it once the session exists, and a startup that never binds
// keeps the checkout while telling the operator how to remove it.
type interactiveWorktreeStartup struct {
	manager  *worktree.WorktreeManager
	checkout worktree.ManagedWorktree
	bound    bool
	reported bool
}

// prepareInteractiveWorktree allocates the managed checkout for `codex --worktree` and moves
// the launch cwd into it. It returns nil when the flag is absent.
func prepareInteractiveWorktree(root *cli.RootOptions) (*interactiveWorktreeStartup, error) {
	if root == nil || !root.Shared.Worktree {
		return nil, nil
	}
	settings, ok := interactiveWorktreeSettings(root)
	if !ok {
		return nil, errors.New("managed worktrees are unavailable: [desktop] worktree settings could not be loaded")
	}
	source := strings.TrimSpace(root.Shared.CWD)
	if source == "" {
		resolved, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("--worktree requires a working directory: %w", err)
		}
		source = resolved
	}
	// Rust #43069: an explicitly untrusted source is refused before any
	// checkout is allocated, so the launch never creates a tree from it.
	if interactiveWorktreeSourceUntrusted(root, source) {
		return nil, errors.New("`--worktree` cannot create a checkout from an explicitly untrusted source")
	}
	manager := worktree.NewWorktreeManager(settings)
	// Rust #43069 anchors relative --add-dir values to the invocation directory
	// before moving the launch cwd into the checkout.
	for i, path := range root.Shared.AddDirs {
		if filepath.IsAbs(path) {
			continue
		}
		resolved, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve --add-dir %q: %w", path, err)
		}
		root.Shared.AddDirs[i] = resolved
	}
	base := ""
	if resolved, err := worktree.DefaultWorktreeBase(source); err == nil {
		base = resolved
	}
	checkout, err := manager.Create(source, base)
	if err != nil {
		return nil, err
	}
	root.Shared.CWD = checkout.CWD
	return &interactiveWorktreeStartup{manager: manager, checkout: checkout}, nil
}

// interactiveForkWorktreeStartup is the destination and forked thread created
// before an interactive `codex fork <ID> --worktree` opens its TUI.
type interactiveForkWorktreeStartup struct {
	worktree *interactiveWorktreeStartup
	forked   *session.Record
}

// sessionWorktreeRequested reports a --worktree flag in either clap position.
// Rust merges the subcommand CLI into the interactive launch, so
// `codex --worktree fork <id>` and `codex fork <id> --worktree` are equivalent.
func sessionWorktreeRequested(root *cli.RootOptions, opts *cli.SessionOptions) bool {
	return (root != nil && root.Shared.Worktree) || (opts != nil && opts.Shared.Worktree)
}

// interactiveSessionRoot merges the fork subcommand flags into the root shape
// consumed by the interactive TUI. The subcommand wins for scalar options;
// repeatable options retain both positions in command-line order.
func interactiveSessionRoot(root *cli.RootOptions, opts *cli.SessionOptions) *cli.RootOptions {
	merged := cli.RootOptions{}
	if root != nil {
		merged = *root
	}
	if opts == nil {
		return &merged
	}
	base, override := merged.Shared, opts.Shared
	choice := func(first, second string) string {
		if strings.TrimSpace(second) != "" {
			return second
		}
		return first
	}
	merged.Shared = cli.SharedOptions{
		Images:                               append(append([]string(nil), base.Images...), override.Images...),
		Model:                                choice(base.Model, override.Model),
		ModelReasoningEffort:                 choice(base.ModelReasoningEffort, override.ModelReasoningEffort),
		OSS:                                  base.OSS || override.OSS,
		OSSProvider:                          choice(base.OSSProvider, override.OSSProvider),
		Profile:                              choice(base.Profile, override.Profile),
		Sandbox:                              choice(base.Sandbox, override.Sandbox),
		ApprovalPolicy:                       choice(base.ApprovalPolicy, override.ApprovalPolicy),
		Search:                               base.Search || override.Search,
		NoAltScreen:                          base.NoAltScreen || override.NoAltScreen,
		NoDaemon:                             base.NoDaemon || override.NoDaemon,
		DangerouslyBypassApprovalsAndSandbox: base.DangerouslyBypassApprovalsAndSandbox || override.DangerouslyBypassApprovalsAndSandbox,
		DangerouslyBypassHookTrust:           base.DangerouslyBypassHookTrust || override.DangerouslyBypassHookTrust,
		ApproveForMe:                         base.ApproveForMe || override.ApproveForMe,
		CWD:                                  choice(base.CWD, override.CWD),
		AddDirs:                              append(append([]string(nil), base.AddDirs...), override.AddDirs...),
		Worktree:                             base.Worktree || override.Worktree,
	}
	merged.ConfigOverrides = append(append([]string(nil), merged.ConfigOverrides...), opts.ConfigOverrides...)
	merged.StrictConfig = merged.StrictConfig || opts.StrictConfig
	merged.Prompt = strings.TrimSpace(opts.Prompt)
	return &merged
}

// runInteractiveForkWorktree is the TUI launch path for
// `codex fork <id> --worktree`. It keeps the checkout and reports recovery
// instructions if startup exits before the forked thread is bound.
func runInteractiveForkWorktree(ctx context.Context, root *cli.RootOptions, opts *cli.SessionOptions, store *session.Store, stdin io.Reader, stdout, stderr io.Writer) error {
	startup, err := prepareInteractiveForkWorktree(root, opts, store)
	if startup != nil && startup.worktree != nil {
		defer startup.worktree.reportStartupFailure(stderr)
	}
	if err != nil {
		return err
	}
	return runInteractiveTUI(ctx, root, stdin, stdout, stderr, startup.worktree, startup.forked)
}

var interactiveForkWorktreeStarter = runInteractiveForkWorktree

// prepareInteractiveForkWorktree resolves the source conversation before
// allocating a checkout. Rust #43069 uses the source thread's newest saved
// turn cwd unless --cd is explicit, forks the conversation into the checkout,
// and binds checkout ownership before the first turn.
func prepareInteractiveForkWorktree(root *cli.RootOptions, opts *cli.SessionOptions, store *session.Store) (*interactiveForkWorktreeStartup, error) {
	if root == nil {
		return nil, errors.New("interactive fork worktree requires launch options")
	}
	if store == nil {
		return nil, errors.New("interactive fork worktree requires a session store")
	}
	target, err := resolveSessionTarget(store, opts)
	if err != nil {
		return nil, err
	}
	source, err := store.Read(target, true, true)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(root.Shared.CWD) == "" {
		root.Shared.CWD = latestInteractiveForkSourceCWD(store, source)
	}
	if strings.TrimSpace(root.Shared.CWD) == "" {
		return nil, errors.New("interactive fork worktree requires a source working directory")
	}

	startup := &interactiveForkWorktreeStartup{}
	managed, err := prepareInteractiveWorktree(root)
	startup.worktree = managed
	if err != nil {
		return startup, err
	}
	if managed == nil {
		return startup, errors.New("interactive fork worktree did not allocate a checkout")
	}
	forked, err := store.Fork(target, session.ForkOptions{Mode: session.ForkAll})
	if err != nil {
		return startup, err
	}
	worktreeCWD := managed.checkout.CWD
	forked, err = store.UpdateMetadata(forked.ID, &session.MetadataPatch{CWD: &worktreeCWD}, false)
	if err != nil {
		return startup, fmt.Errorf("move forked session into managed worktree: %w", err)
	}
	if err := managed.bind(string(forked.ID)); err != nil {
		return startup, err
	}
	startup.forked = forked
	return startup, nil
}

// latestInteractiveForkSourceCWD mirrors Rust worktree_startup::latest_thread_cwd:
// the newest persisted turn_context cwd wins, with the thread record cwd as
// the fallback when the rollout is absent or has no turn context.
func latestInteractiveForkSourceCWD(store *session.Store, record *session.Record) string {
	if store == nil || record == nil {
		return ""
	}
	for _, archived := range []bool{false, true} {
		path, err := rollout.FindThreadPath(sessionCodexHome(store), string(record.ID), archived)
		if err != nil {
			continue
		}
		if cwd := latestInteractiveRolloutTurnContextCWD(path); cwd != "" {
			return cwd
		}
		break
	}
	return strings.TrimSpace(record.Metadata.CWD)
}

type interactiveForkTurnContextLine struct {
	Type    string `json:"type"`
	Payload struct {
		CWD string `json:"cwd"`
	} `json:"payload"`
	TurnContext struct {
		CWD string `json:"cwd"`
	} `json:"turn_context"`
}

func latestInteractiveRolloutTurnContextCWD(path string) string {
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
		var line interactiveForkTurnContextLine
		if err := json.Unmarshal(raw, &line); err != nil || line.Type != "turn_context" {
			continue
		}
		cwd := strings.TrimSpace(line.Payload.CWD)
		if cwd == "" {
			cwd = strings.TrimSpace(line.TurnContext.CWD)
		}
		if cwd != "" {
			latest = cwd
		}
	}
	return latest
}

// interactiveWorktreeSourceUntrusted reports an explicit "untrusted" decision for
// the source project; tests replace it to exercise the refusal branch without
// writing a projects table.
var interactiveWorktreeSourceUntrusted = func(root *cli.RootOptions, source string) bool {
	loaded, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), interactiveKeymapLoadOptions(root))
	if err != nil || loaded == nil {
		return false
	}
	level, ok := config.ProjectTrustLevelForTarget(loaded.Values, source)
	return ok && strings.EqualFold(strings.TrimSpace(level), "untrusted")
}

// bind records the checkout's owning thread so later cleanup can attribute it.
func (s *interactiveWorktreeStartup) bind(threadID string) error {
	if s == nil || s.bound {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return errors.New("managed worktree requires a thread id")
	}
	if err := s.manager.BindThread(s.checkout.Root, threadID); err != nil {
		return fmt.Errorf("failed to bind managed worktree thread: %w", err)
	}
	s.bound = true
	return nil
}

// reportStartupFailure prints Rust's StartupRecovery message when the launch
// exits before a thread was bound. The checkout stays on disk on purpose.
func (s *interactiveWorktreeStartup) reportStartupFailure(stderr io.Writer) {
	if s == nil || s.bound || s.reported || stderr == nil {
		return
	}
	s.reported = true
	fmt.Fprintf(stderr, "Startup did not finish binding a thread to this worktree: %q\n"+
		"The checkout was kept. Inspect it and confirm no session is using it.\n"+
		"To remove it, run `git worktree remove <checkout-path>` from the source repository,\n"+
		"replacing <checkout-path> with the path above. Do not use --force.\n",
		s.checkout.Root)
}
