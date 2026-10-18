# Round 667：late async score approval race audit（#51396）

Rust 依据：`57d57df60845f6f59e1c30c71210c8af5246b32d`。

## 审计结论

该提交依赖 Rust async Guardian scorer 的 score-update channel：

- adaptive mode 订阅 `GuardianV2ScoreProgress.updates`，将 low-risk cached
  evidence 与 synchronous review 竞速；
- 异步 approve 获胜时取消并 drain review，再 revalidate action/history lifetime。

Go 没有该结构：

- `modelGuardianReviewer.Review` 是同步路径；`scoreProgress` 只记录 tool-call
  位置与 lag，不发布可等待的 score update。
- 现有 `fastDecision` 只用于 full-access 与 user-approval 的快速批准，不是
  async score/cached-approval race。
- 没有 `cached_approval_is_current`、`GuardianReviewOutcome::CachedApproval` 或
  可取消的 pending synchronous review handle。

因此 Go 没有需要竞速的 async approval 路径，也没有可移植的 landing site。

## 验证

```text
rg -n 'async.*scor|async.*classif|scoreProgress|fastDecision|cached_approval' --glob '*.go'
```
