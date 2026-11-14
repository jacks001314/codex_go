# TUI checkout-dependent test failures

The full uncached run reported four failures in tui/tea. Its snapshots and fixed scroll/voice-fit thresholds used the process working directory, which in this Syntropy checkout is long enough to wrap the status footer into extra rows. Multiline status is intentional and has existing row-budget regression coverage.

Assigned CWD=/repo to the fixed-layout snapshot, pinned prompt and voice composer fixtures. No production rendering behavior or expected snapshots changed. Also normalized touched test import grouping.

Targeted tests for pinned prompts, terminal snapshots, voice strip, VT100 snapshots and multiline status budgets passed (0.163s). go vet ./tui/tea exited 0. Full tui/tea uncached test is running in session 4452 with log tui_full_after_paths_2026_11_14.log; do not count it as passed yet.

The broader upstream synchronization remains incomplete, including native Linux verification and remaining plan entries. No commit/push has been made.
