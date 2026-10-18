# Round 725: bwrap and Seatbelt integrity backend audit

- `#51841` macOS Seatbelt backend：Go `sandbox/integrity` 在 darwin 选择
  `MacosSeatbelt`，inventory Codex executable 与 launcher argv（sandbox-exec），
  `PreparePolicy` 保留 native deny globs、不做 snapshot scan。审计为 complete。
- `#51840` bubblewrap backend：Go 已 inventory Codex executable 与 launcher
  command（bwrap），并支持 `~/` deny glob 解析；但仍缺少 command PATH 上的
  `rg`/GLOB_SCAN_PROGRAM 发现、bundled/system bwrap candidate inventory，以及
  `:tmpdir`/TMPDIR 的显式 command environment 解析，因为 `RunnerRequest` 当前
  不携带 env。审计为 partial，并记录剩余缺口。

验证：

- `go test ./sandbox/integrity -count=1`
- `GOOS=darwin GOARCH=arm64 go test -c ./sandbox`
- `GOOS=linux GOARCH=amd64 go build ./...`
