# Guardian and daemon module-refactor audits

Rust upstream refactors:

- `#45521` Move Guardian reviewer startup into the pool
- `#45546` Move daemon packages out of the standalone CLI installation
- `#45491` Remove Guardian subagent-spawner plumbing
- `#45418` Extract Guardian conversation bookkeeping into the reviewer crate
- `#45518` Route Guardian reviewers through ThreadManager for inline parents
- `#44544` Move Guardian reporting and denial accounting into the extension

## Verdict

All are not applicable as Go architecture mismatches. Go starts and routes
Guardian reviews through the synchronous appserver reviewer/agent runtime,
keeps bookkeeping in `appserver/guardian_review_history.go` and
`state/guardian_retained_context.go`, records metrics directly in appserver/state,
and ships daemon code as first-class `appserverdaemon` modules. There is no
Rust extension pool, extension reviewer crate, spawner plumbing, or CLI package
installation tree to reproduce.
