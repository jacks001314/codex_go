# Host-only MCP contribution context

Added NewHostMCPServerContributionContext(threadID, originator, sessionSource) with explicit empty disabled-plugin snapshot, plus HostOnly and SessionSource accessors. Host projection skips activeStepCapabilitySnapshot executor plugin inventory. Global runtimeMCPConfig now supplies an explicit empty host context. Existing thread isolation/MCP disable checks are retained.

Expanded actual plugin projection regression: host context does not inherit parent's live disabled list; host context with explicit beta denial excludes beta. Existing explicit override, empty, unrelated-thread, originator and prediction-fork regressions pass. go test -count=1 -timeout 3m ./appserver ./mcp -run 'TestMCPExplicit|TestContributionContext|TestPredictionForkMCPProjection' passed 0.159s/0.044s; go vet ./appserver ./mcp exited 0.

Remaining #61283: test a populated live executor inventory is excluded, verify pending environment restrictions and actual HTTP originator delivery; SessionSource is carried but no new consumer has been wired. Therefore this is partial progress, not overall completion. No commit/push.
