# Round 116 · #43147 experimental context model-capability gate

Rust source: `6af345407d`.

## Status

The behavioral requirement is present in Go; this round verified it and closed
the ledger row.

- `model.ModelInfo.SupportsExperimentalContext` exists, defaults to false for
  omitted metadata, and is carried from the catalog
  (`model/catalog.go`, `model_test` asserts the default).
- `RuntimeRouter.experimentalContextManagementEligible` requires the
  `context_management` feature + `experimental_mode`, an OpenAI provider, the
  starting model's `supports_experimental_context`, a ChatGPT backend, and an
  eligible account plan. An unresolved model stays ineligible.
- `newContextWindowRequesterForTurn` and `token_budget_history_notes.go` apply
  the same gate before offering experimental context behavior.

## TokenBudgetStartupConfig

Rust snapshots configured token-budget preferences before a session mutates its
feature/config state, so fresh child sessions can restore them. Go does not
mutate `Config` feature settings at session startup: token-budget activation is
derived from the effective config on every use
(`features.Enabled(cfg.FeatureSettings(), "token_budget")` or the experimental
gate), and child sessions load their own effective config rather than
inheriting a parent's activated flag. There is therefore no mutable startup
state to snapshot or restore, and no `TokenBudgetStartupConfig` equivalent is
needed.

## Verification

```text
go test ./model -run 'TestModelInfoDefaults|Experimental' -count=1  # ok
go test ./appserver -run 'TokenBudget|ExperimentalContext|ContextWindow' -count=1  # ok
go build ./...  # ok
```
