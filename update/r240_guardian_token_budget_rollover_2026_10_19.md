# Guardian review token-budget rollover

Rust upstream: `2d929eb7c39a612b84e0987f2af4a4c2282249e2` -
`Honor turn token budgets in Guardian review rollover (#41221)`.

## Alignment

Before a Guardian follow-up review, Rust checks whether the review model's
context-window token status has reached its rollover limit. The budget is
resolved from the parent turn's captured token-budget preferences and its
model-default flag, using the review model's defaults. This preserves explicit
turn preferences instead of replacing them with review-model defaults.

Go now performs the same decision before constructing the review request:

- it resolves the parent turn's captured budget against the review model's
  token-budget defaults;
- it applies the resolved auto-compact fallback buffer to the Code Mode/context
  compaction policy used for the review model;
- when the resulting status requires compaction, it clears the cached reviewer
  response chain so the next review starts fresh.

## Verification

```text
go test ./appserver -run 'GuardianTokenBudgetRollover|TokenBudget' -count=1
go test ./parity -count=1
```
