# Reconstructed session lineage (#61183)

Exact upstream patches downloaded: upstream_61183.patch and upstream_61216.patch. This change implements part of #61183; #61216 was inspected but not implemented this turn.

ThreadReadSnapshot now accepts ForkedFromID. SessionStateForThreadRead uses the target lineage rather than inheriting the primary session's parent. Shared setForkParent uses the existing UUID parser, clears cached parent title on change, treats invalid/absent IDs as no parent, and copies the parsed ID. Prompt-edit response reconstruction also uses it instead of aliasing the response pointer and leaving a stale title.

Regression covers changed/absent/invalid/same parents, current permission preservation, source immutability and parent pointer independence. The first run failed on inherited parent and stale title cases. After implementation, `go test ./tui/app -count=1` passes (0.129s) and `go vet ./tui/app` exits 0. Logs: thread_lineage_red_2026_11_14.log and thread_lineage_green_2026_11_14.log.

Limit: SessionStateForThreadRead currently has test callers but no production caller found in app/tui search. Existing prompt-edit response path is connected. Rust also reconstructs state from ThreadStarted notifications; Go's actual TUI notification/reconstruction integration must be audited before #61183 can be declared complete. No full-module test or live TUI verification was run for this change. Overall synchronization and commit/push remain incomplete.
