# Round 694 ledger consistency audit

- 删除 `#54432` 的陈旧 partial 行；保留其 Round 317 complete 记录。
- 删除 `#49401` 的陈旧 partial 行；保留其 Round 666 complete 记录。
- 重新扫描 ledger 后，不存在同一 SHA 同时含 partial 与 complete 的重复行。

验证：

- PowerShell `Import-Csv` 按 SHA 分组并筛选 partial+complete 重复项
