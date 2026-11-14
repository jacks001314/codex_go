# TUI delegation retention (#61210)

Upstream f562a7b3e23302c36964baf2770c98e1e3b0091f changes the TUI start_turn tool output to retain=true. Exact patch: upstream_61210.patch.

Go app/dynamic_tools_helpers.go now sets Retain on the shared startTurn path. New WebSocket request tests invoke both create_thread and send_message_to_thread through the dynamic tool executor, asserting the decoded retain flag, namespace/tool identity and escaped delegation content. Both cases failed before the production change and pass afterward.

Validation:
- go test ./app ./realtime ./tui/tea -count=1 -timeout 8m: exit 0; app 72.456s, realtime 4.580s, tui/tea 66.424s. Includes latest voice cwd and SPIFFE changes in dependencies.
- go vet ./app ./realtime ./tui/tea: exit 0, no output.
- go test ./appserver -run TestStandaloneToolOutputRetentionAnnotationLikeRust -count=1: exit 0, 0.103s. Confirms retained output maps to client_authored harness metadata.

Logs: delegation_retention_red_2026_11_14.log, delegation_voice_integrated_2026_11_14.log, delegation_retention_backend_2026_11_14.log.

This completes the request flag change in #61210. Overall synchronization remains incomplete. These are automated local tests, not live-model or native audio certification. No commit/push performed.
